package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Skill struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Files    []string `json:"files"`
	Root     string   `json:"root"`
}

func Scan(roots []string) ([]Skill, error) {
	items := map[string]*Skill{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			category := entry.Name()
			categoryDir := filepath.Join(root, category)
			files, err := os.ReadDir(categoryDir)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				name := baseSkillName(file.Name())
				if name == "" {
					continue
				}
				key := category + ":" + name
				item, ok := items[key]
				if !ok {
					item = &Skill{
						Name:     name,
						Category: category,
						Root:     root,
					}
					items[key] = item
				}
				item.Files = append(item.Files, filepath.Join(categoryDir, file.Name()))
			}
		}
	}
	out := make([]Skill, 0, len(items))
	for _, item := range items {
		sort.Strings(item.Files)
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category == out[j].Category {
			return out[i].Name < out[j].Name
		}
		return out[i].Category < out[j].Category
	})
	return out, nil
}

func baseSkillName(filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	if base == "" {
		return ""
	}
	switch base {
	case "README", "TODO", "AGENTS", "CRITICAL-RULES", "QQ-DevTools-Guide", "link-skills", "help":
		return ""
	}
	return base
}
