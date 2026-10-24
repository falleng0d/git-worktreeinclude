package engine

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// The IDE keeps tabs and layout in <config>/<product>/workspace/<ProjectId>.xml, so worktrees sharing a ProjectId overwrite each other.
const (
	jetbrainsWorkspaceRel = ".idea/workspace.xml"
	ideStateOp            = "ide-state"
	ideStatePath          = ".idea"
	projectIDLength       = 27
	ksuidEpoch            = 1400000000
	pathTerminators       = "/\\\"'&<>;,:()[]{} \t\r\n"
)

var (
	windowsPaths     = runtime.GOOS == "windows"
	projectIDPattern = regexp.MustCompile(`<component name="ProjectId" id="([0-9A-Za-z]{1,27})"`)
	framePattern     = regexp.MustCompile(`<frame [^>]*/>`)
)

func jetbrainsConfigRoots() []string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(dir, "JetBrains"), filepath.Join(dir, "Google")}
}

func readProjectID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return projectIDFrom(string(data)), nil
}

func projectIDFrom(content string) string {
	m := projectIDPattern.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return m[1]
}

// newProjectID returns a KSUID, the form the IDE generates and accepts.
func newProjectID() (string, error) {
	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf, uint32(time.Now().Unix()-ksuidEpoch))
	if _, err := rand.Read(buf[4:]); err != nil {
		return "", err
	}
	id := new(big.Int).SetBytes(buf).Text(62)
	return strings.Repeat("0", projectIDLength-len(id)) + id, nil
}

func previousProjectID(prep prepared) string {
	sourceID, err := readProjectID(filepath.Join(prep.sourceRoot, filepath.FromSlash(jetbrainsWorkspaceRel)))
	if err != nil {
		return ""
	}
	targetID, err := readProjectID(filepath.Join(prep.targetRoot, filepath.FromSlash(jetbrainsWorkspaceRel)))
	if err != nil || targetID == sourceID {
		return ""
	}
	return targetID
}

func (e *Engine) splitJetBrainsProject(sourceRoot, targetRoot, reuseID string, dryRun bool) (Action, bool) {
	errAction := Action{Op: ideStateOp, Path: ideStatePath, Status: "error"}

	sourceID, err := readProjectID(filepath.Join(sourceRoot, filepath.FromSlash(jetbrainsWorkspaceRel)))
	if err != nil {
		return errAction, true
	}
	if sourceID == "" {
		return Action{}, false
	}

	targetWorkspace := filepath.Join(targetRoot, filepath.FromSlash(jetbrainsWorkspaceRel))
	targetData, err := os.ReadFile(targetWorkspace)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && dryRun {
			return Action{Op: ideStateOp, Path: ideStatePath, Status: "planned"}, true
		}
		if errors.Is(err, os.ErrNotExist) {
			return Action{}, false
		}
		return errAction, true
	}
	targetID := projectIDFrom(string(targetData))
	if targetID == "" {
		return Action{}, false
	}
	if targetID != sourceID {
		return Action{Op: ideStateOp, Path: ideStatePath, Status: "same"}, true
	}
	if dryRun {
		return Action{Op: ideStateOp, Path: ideStatePath, Status: "planned"}, true
	}

	newID := reuseID
	if newID == "" {
		newID, err = newProjectID()
		if err != nil {
			return errAction, true
		}
	}

	toTarget := newPathRewriter(sourceRoot, targetRoot)
	if err := copyIDEState(e.ideConfigRoots, sourceID, newID, sourceRoot, targetRoot, toTarget); err != nil {
		return errAction, true
	}

	// Written last so a failed state copy leaves the shared id in place and a rerun retries.
	info, err := os.Stat(targetWorkspace)
	if err != nil {
		return errAction, true
	}
	updated := toTarget.rewrite(strings.ReplaceAll(string(targetData), sourceID, newID))
	if err := writeFileAtomic(targetWorkspace, []byte(updated), info.Mode().Perm()); err != nil {
		return errAction, true
	}
	return Action{Op: ideStateOp, Path: ideStatePath, Status: "done"}, true
}

func copyIDEState(configRoots []string, sourceID, newID, sourceRoot, targetRoot string, toTarget pathRewriter) error {
	for _, root := range configRoots {
		products, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		for _, product := range products {
			if !product.IsDir() {
				continue
			}
			productDir := filepath.Join(root, product.Name())
			state, err := os.ReadFile(filepath.Join(productDir, "workspace", sourceID+".xml"))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			rewritten := toTarget.rewrite(strings.ReplaceAll(string(state), sourceID, newID))
			if err := writeFileAtomic(filepath.Join(productDir, "workspace", newID+".xml"), []byte(rewritten), 0o644); err != nil {
				return err
			}
			if err := addRecentProject(filepath.Join(productDir, "options", "recentProjects.xml"), sourceRoot, targetRoot, newID); err != nil {
				return err
			}
		}
	}
	return nil
}

// A running IDE rewrites recentProjects.xml from memory and drops this entry.
func addRecentProject(path, sourceRoot, targetRoot, newID string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	content := string(data)

	targetKey := recentProjectKey(targetRoot)
	if indexPath(content, entryOpenTag(targetKey)) >= 0 || indexPath(content, entryOpenTag(filepath.ToSlash(targetRoot))) >= 0 {
		return nil
	}
	sourceEntry := findEntry(content, recentProjectKey(sourceRoot))
	if sourceEntry == "" {
		sourceEntry = findEntry(content, filepath.ToSlash(sourceRoot))
	}
	frame := framePattern.FindString(sourceEntry)
	if frame == "" {
		return nil
	}

	mapStart := strings.Index(content, `<option name="additionalInfo">`)
	if mapStart < 0 {
		return nil
	}
	mapEnd := strings.Index(content[mapStart:], "</map>")
	if mapEnd < 0 {
		return nil
	}
	mapEnd += mapStart
	lineStart := strings.LastIndex(content[:mapEnd], "\n") + 1
	indent := content[lineStart:mapEnd]
	if strings.TrimSpace(indent) != "" {
		return nil
	}

	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	in := indent + "  "
	entry := strings.Join([]string{
		in + entryOpenTag(targetKey),
		in + "  <value>",
		in + `    <RecentProjectMetaInfo projectWorkspaceId="` + newID + `">`,
		in + "      " + frame,
		in + "    </RecentProjectMetaInfo>",
		in + "  </value>",
		in + "</entry>",
	}, nl) + nl
	updated := content[:lineStart] + entry + content[lineStart:]

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(updated), info.Mode().Perm())
}

func recentProjectKey(root string) string {
	return collapseHome(filepath.ToSlash(root))
}

func entryOpenTag(key string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(key))
	return `<entry key="` + b.String() + `">`
}

func findEntry(content, key string) string {
	start := indexPath(content, entryOpenTag(key))
	if start < 0 {
		return ""
	}
	end := strings.Index(content[start:], "</entry>")
	if end < 0 {
		return ""
	}
	return content[start : start+end]
}

func collapseHome(slashPath string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return slashPath
	}
	home = filepath.ToSlash(home)
	if len(slashPath) > len(home) && indexPath(slashPath[:len(home)+1], home+"/") == 0 {
		return "$USER_HOME$" + slashPath[len(home):]
	}
	return slashPath
}

func indexPath(s, sub string) int {
	if !windowsPaths {
		return strings.Index(s, sub)
	}
	return strings.Index(asciiLower(s), asciiLower(sub))
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

type pathRewriter struct {
	pairs [][2]string
}

func newPathRewriter(from, to string) pathRewriter {
	from = filepath.ToSlash(from)
	to = filepath.ToSlash(to)
	r := pathRewriter{pairs: [][2]string{{from, to}}}
	if collapsedFrom := collapseHome(from); collapsedFrom != from {
		r.pairs = append(r.pairs, [2]string{collapsedFrom, collapseHome(to)})
	}
	if windowsPaths {
		nativeFrom := strings.ReplaceAll(from, "/", `\`)
		nativeTo := strings.ReplaceAll(to, "/", `\`)
		r.pairs = append(r.pairs,
			[2]string{nativeFrom, nativeTo},
			[2]string{strings.ReplaceAll(nativeFrom, `\`, `\\`), strings.ReplaceAll(nativeTo, `\`, `\\`)},
		)
	}
	return r
}

func (r pathRewriter) rewrite(s string) string {
	for _, pair := range r.pairs {
		s = replaceRootPath(s, pair[0], pair[1])
	}
	return s
}

// Matches only at a path component end, so /repo does not match inside /repo-2.
func replaceRootPath(s, from, to string) string {
	if from == "" || from == to {
		return s
	}
	var b strings.Builder
	for {
		i := indexPath(s, from)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(from)
		b.WriteString(s[:i])
		if end == len(s) || strings.IndexByte(pathTerminators, s[end]) >= 0 {
			b.WriteString(to)
		} else {
			b.WriteString(s[i:end])
		}
		s = s[end:]
	}
}

func jetbrainsWorkspaceSame(srcPath, dstPath, sourceRoot, targetRoot string) (bool, error) {
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return false, err
	}
	dst, err := os.ReadFile(dstPath)
	if err != nil {
		return false, err
	}
	sourceID := projectIDFrom(string(src))
	targetID := projectIDFrom(string(dst))
	normalized := string(dst)
	if sourceID != "" && targetID != "" && targetID != sourceID {
		normalized = newPathRewriter(targetRoot, sourceRoot).rewrite(strings.ReplaceAll(normalized, targetID, sourceID))
	}
	return normalized == string(src), nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	parent := filepath.Dir(path)
	tmp, err := os.CreateTemp(parent, ".git-worktreeinclude-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
