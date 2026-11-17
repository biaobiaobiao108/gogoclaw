package skills

import (
	"path/filepath"

	"github.com/biaobiaobiao108/gogoclaw/pkg/appstate"
)

// Origin metadata is stored alongside the workspace state and keyed by the skill directory.
func LoadOrigin(workspace, skillDir string) ([]byte, error) {
	db, err := appstate.Open(filepath.Join(workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Get("skill_origin", filepath.Base(skillDir))
}

func SaveOrigin(workspace, skillDir string, data []byte) error {
	db, err := appstate.Open(filepath.Join(workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Put("skill_origin", filepath.Base(skillDir), data)
}

func DeleteOrigin(workspace, skillDir string) error {
	db, err := appstate.Open(filepath.Join(workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Delete("skill_origin", filepath.Base(skillDir))
}
