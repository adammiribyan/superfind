package session

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
)

const claudeWorktrees = ".claude/worktrees"

func EditedFiles(values []string) map[string]bool {
	sessions := map[string]bool{}
	db, err := OpenSearch()
	if err != nil {
		return sessions
	}
	defer db.Close()
	for _, value := range values {
		exact, like := places(value)
		for _, path := range exact {
			pairs, err := QueryPairs(db, "SELECT DISTINCT session, '' FROM files WHERE how = ? AND NOT failed AND "+
				"(path = ? OR (path > ? AND path < ?))", Edited, path, path+"/", path+"0")
			if err == nil {
				for _, p := range pairs {
					sessions[p[0]] = true
				}
			}
		}
		for _, w := range like {
			pairs, err := QueryPairs(db, "SELECT DISTINCT session, '' FROM files WHERE how = ? AND NOT failed AND path > ? AND path < ? AND "+
				`(path LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`, Edited, w.under+"/", w.under+"0", w.pattern, w.pattern+"/%")
			if err == nil {
				for _, p := range pairs {
					sessions[p[0]] = true
				}
			}
		}
	}
	return sessions
}

type worktreeFile struct{ under, pattern string }

func places(value string) (exact []string, like []worktreeFile) {
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(agent.Home(), value[2:])
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return nil, nil
	}
	exact = []string{path}
	dir := agent.SurvivingDir(path)
	if real := filepath.Join(agent.Resolve(dir), strings.TrimPrefix(path, dir)); real != path {
		exact = append(exact, real)
	}
	listed, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return exact, nil
	}
	var roots []string
	for _, l := range strings.Split(string(listed), "\n") {
		if root, ok := strings.CutPrefix(l, "worktree "); ok {
			roots = append(roots, root)
			if real := agent.Resolve(root); real != root {
				roots = append(roots, real)
			}
		}
	}
	rel, found := "", false
	for _, candidate := range exact {
		for _, root := range roots {
			if r, err := filepath.Rel(root, candidate); err == nil && r != ".." && !strings.HasPrefix(r, "../") && (!found || len(r) < len(rel)) {
				rel, found = r, true
			}
		}
	}
	if !found {
		return exact, nil
	}
	for _, candidate := range slices.Clone(exact) {
		if rel == "." {
			roots = append(roots, candidate)
		} else if root, ok := strings.CutSuffix(candidate, "/"+rel); ok {
			roots = append(roots, root)
		}
	}
	slices.Sort(roots)
	for _, root := range slices.Compact(roots) {
		under := filepath.Join(root, claudeWorktrees)
		if rel == "." {
			exact = append(exact, root)
			like = append(like, worktreeFile{under, escapeLike(under) + "/%"})
		} else {
			exact = append(exact, filepath.Join(root, rel))
			like = append(like, worktreeFile{under, escapeLike(under) + "/%/" + escapeLike(rel)})
		}
	}
	slices.Sort(exact)
	return slices.Compact(exact), like
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}
