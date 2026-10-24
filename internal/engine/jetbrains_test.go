package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/falleng0d/git-worktreeinclude/internal/exitcode"
)

const testSourceProjectID = "3EqgbLMQR4pBiiKp0giri2rkNho"

type jetbrainsFixture struct {
	engineFixture
	productDir string
}

func TestEngineApplyGivesJetBrainsWorktreeItsOwnProjectID(t *testing.T) {
	fx := setupJetBrainsFixture(t)
	e := NewEngine()
	e.ideConfigRoots = []string{filepath.Dir(fx.productDir)}

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{From: "auto", Include: testIncludeFile, DryRun: true})
	if err != nil || code != exitcode.OK {
		t.Fatalf("dry-run Apply = code %d, err %v", code, err)
	}
	if action := findAction(t, res.Actions, ideStatePath); action.Status != "planned" {
		t.Fatalf("expected planned IDE action, got %+v", action)
	}
	if got := workspaceStateFiles(t, fx.productDir); len(got) != 1 {
		t.Fatalf("dry-run must not write IDE state, got %v", got)
	}

	res, code, err = e.Apply(context.Background(), fx.wt, ApplyOptions{From: "auto", Include: testIncludeFile})
	if err != nil || code != exitcode.OK {
		t.Fatalf("Apply = code %d, err %v, actions %+v", code, err, res.Actions)
	}
	if action := findAction(t, res.Actions, ideStatePath); action.Status != "done" {
		t.Fatalf("expected done IDE action, got %+v", action)
	}

	source, target := filepath.ToSlash(res.From), filepath.ToSlash(res.To)
	workspace := readFile(t, filepath.Join(fx.wt, ".idea", "workspace.xml"))
	newID := projectIDFrom(workspace)
	if newID == "" || newID == testSourceProjectID || len(newID) != projectIDLength {
		t.Fatalf("expected a new 27 character project id, got %q", newID)
	}
	if strings.Contains(workspace, testSourceProjectID) {
		t.Fatalf("target workspace.xml still references the source id:\n%s", workspace)
	}
	if !strings.Contains(workspace, `key="_`+target+`" value="`+newID+`"`) {
		t.Fatalf("expected path keyed entry rewritten to target:\n%s", workspace)
	}
	if got := readFile(t, filepath.Join(fx.root, ".idea", "workspace.xml")); projectIDFrom(got) != testSourceProjectID {
		t.Fatalf("source workspace.xml must keep its id, got %q", projectIDFrom(got))
	}

	state := readFile(t, filepath.Join(fx.productDir, "workspace", newID+".xml"))
	if !strings.Contains(state, target+"/notes.md") || !strings.Contains(state, source+"-3/other.md") {
		t.Fatalf("expected root paths rewritten and sibling paths kept:\n%s", state)
	}
	if strings.Contains(state, source+"/notes.md") {
		t.Fatalf("copied state still points at the source root:\n%s", state)
	}

	recent := readFile(t, filepath.Join(fx.productDir, "options", "recentProjects.xml"))
	entry := findEntry(recent, recentProjectKey(target))
	if !strings.Contains(entry, `projectWorkspaceId="`+newID+`"`) || !strings.Contains(entry, `<frame x="213" y="30" width="2134" height="1379" />`) {
		t.Fatalf("expected recent project entry with copied frame, got %q in:\n%s", entry, recent)
	}

	res, code, err = e.Apply(context.Background(), fx.wt, ApplyOptions{From: "auto", Include: testIncludeFile})
	if err != nil || code != exitcode.OK {
		t.Fatalf("second Apply = code %d, err %v, actions %+v", code, err, res.Actions)
	}
	if action := findAction(t, res.Actions, jetbrainsWorkspaceRel); action.Status != "same" {
		t.Fatalf("workspace.xml differing only by its own id must count as same, got %+v", action)
	}
	if action := findAction(t, res.Actions, ideStatePath); action.Status != "same" {
		t.Fatalf("expected IDE action to be same on rerun, got %+v", action)
	}

	writeFile(t, filepath.Join(fx.root, ".idea", "workspace.xml"), strings.Replace(readFile(t, filepath.Join(fx.root, ".idea", "workspace.xml")), "</project>", "  <component name=\"Extra\" />\n</project>", 1))
	res, code, err = e.Apply(context.Background(), fx.wt, ApplyOptions{From: "auto", Include: testIncludeFile, Force: true})
	if err != nil || code != exitcode.OK {
		t.Fatalf("forced Apply = code %d, err %v, actions %+v", code, err, res.Actions)
	}
	if got := projectIDFrom(readFile(t, filepath.Join(fx.wt, ".idea", "workspace.xml"))); got != newID {
		t.Fatalf("--force must keep the worktree's own id %q, got %q", newID, got)
	}
	if got := workspaceStateFiles(t, fx.productDir); len(got) != 2 {
		t.Fatalf("expected source and worktree state files only, got %v", got)
	}
}

func TestReplaceRootPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "/a/repo", want: "/a/wt"},
		{in: `"/a/repo/x.md"`, want: `"/a/wt/x.md"`},
		{in: "&quot;/a/repo&quot;", want: "&quot;/a/wt&quot;"},
		{in: "/a/repo-3/x.md", want: "/a/repo-3/x.md"},
		{in: "/a/repo.bak", want: "/a/repo.bak"},
		{in: "/a/repo /a/repo2 /a/repo", want: "/a/wt /a/repo2 /a/wt"},
	}
	for _, tc := range cases {
		if got := replaceRootPath(tc.in, "/a/repo", "/a/wt"); got != tc.want {
			t.Errorf("replaceRootPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPathRewriterWindowsForms(t *testing.T) {
	windowsPaths = true
	t.Cleanup(func() { windowsPaths = runtime.GOOS == "windows" })

	r := newPathRewriter("C:/Users/Me/repo", "C:/Users/Me/repo-2")
	in := `c:/users/me/REPO/a.md C:\Users\Me\repo\b.md {"p":"C:\\Users\\Me\\repo\\c.md"} C:/Users/Me/repo-3/d.md`
	want := `C:/Users/Me/repo-2/a.md C:\Users\Me\repo-2\b.md {"p":"C:\\Users\\Me\\repo-2\\c.md"} C:/Users/Me/repo-3/d.md`
	if got := r.rewrite(in); got != want {
		t.Fatalf("rewrite =\n%s\nwant\n%s", got, want)
	}
}

func setupJetBrainsFixture(t *testing.T) jetbrainsFixture {
	t.Helper()
	fx := setupEngineFixture(t)

	source := strings.TrimSpace(runGit(t, fx.root, "rev-parse", "--show-toplevel"))
	writeFile(t, filepath.Join(fx.root, ".gitignore"), ".env\n.env.local\n.idea/\n")
	writeFile(t, filepath.Join(fx.root, testIncludeFile), ".env\n.env.local\n.idea/\n")
	if err := os.MkdirAll(filepath.Join(fx.root, ".idea"), 0o755); err != nil {
		t.Fatalf("mkdir .idea: %v", err)
	}
	writeFile(t, filepath.Join(fx.root, ".idea", "workspace.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="CopilotPersistence">
    <persistenceIdMap>
      <entry key="_`+source+`" value="`+testSourceProjectID+`" />
    </persistenceIdMap>
  </component>
  <component name="ProjectId" id="`+testSourceProjectID+`" />
</project>
`)

	productDir := filepath.Join(t.TempDir(), "JetBrains", "PyCharm2026.3")
	for _, dir := range []string{"workspace", "options"} {
		if err := os.MkdirAll(filepath.Join(productDir, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFile(t, filepath.Join(productDir, "workspace", testSourceProjectID+".xml"), `<project version="4">
  <component name="IdeDocumentHistory">[&quot;`+source+`/notes.md&quot;, &quot;`+source+`-3/other.md&quot;]</component>
  <component name="FileEditorManager"><file url="file://$PROJECT_DIR$/README.md" /></component>
</project>
`)
	writeFile(t, filepath.Join(productDir, "options", "recentProjects.xml"), `<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="`+filepath.ToSlash(source)+`">
          <value>
            <RecentProjectMetaInfo projectWorkspaceId="`+testSourceProjectID+`">
              <frame x="213" y="30" width="2134" height="1379" />
            </RecentProjectMetaInfo>
          </value>
        </entry>
      </map>
    </option>
  </component>
</application>
`)
	return jetbrainsFixture{engineFixture: fx, productDir: productDir}
}

func workspaceStateFiles(t *testing.T, productDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(productDir, "workspace"))
	if err != nil {
		t.Fatalf("read workspace dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
