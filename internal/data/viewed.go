package data

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// The files marked as viewed in the review mode, kept locally. A file is
// viewed for a given patch: it shows up again once its changes change.
var viewedMu sync.Mutex

func viewedPath() string {
	return filepath.Join(filepath.Dir(NotesDir()), "viewed.json")
}

func loadAllViewed() map[string]map[string]string {
	all := map[string]map[string]string{}
	if b, err := os.ReadFile(viewedPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	return all
}

// PatchHash identifies the changes of a file.
func PatchHash(patch string) string {
	h := sha1.Sum([]byte(patch))
	return hex.EncodeToString(h[:8])
}

// LoadViewed maps the viewed files of a review (a PR url or a worktree
// branch) to the hash of the patch they were viewed with.
func LoadViewed(key string) map[string]string {
	viewedMu.Lock()
	defer viewedMu.Unlock()
	v := loadAllViewed()[key]
	if v == nil {
		v = map[string]string{}
	}
	return v
}

func SaveViewed(key string, viewed map[string]string) error {
	viewedMu.Lock()
	defer viewedMu.Unlock()
	all := loadAllViewed()
	if len(viewed) == 0 {
		delete(all, key)
	} else {
		all[key] = viewed
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(viewedPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(viewedPath(), b, 0o644)
}
