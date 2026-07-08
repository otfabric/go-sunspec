// SPDX-License-Identifier: MIT

package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func ParseDir(dir string) ([]ModelDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("schema: read dir %s: %w", dir, err)
	}

	var models []ModelDef
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "model_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		m, err := parseFile(path, name)
		if err != nil {
			return nil, err
		}
		models = append(models, m)
	}

	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func parseFile(path, name string) (ModelDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelDef{}, fmt.Errorf("schema: read %s: %w", path, err)
	}

	var m ModelDef
	if err := json.Unmarshal(data, &m); err != nil {
		return ModelDef{}, fmt.Errorf("schema: parse %s: %w", path, err)
	}

	if m.ID == 0 {
		m.ID = idFromFilename(name)
	}
	if m.ID == 0 {
		m.ID = idFromPoints(m.Group.Points)
	}

	return m, nil
}

func idFromFilename(name string) int {
	s := strings.TrimPrefix(name, "model_")
	s = strings.TrimSuffix(s, ".json")
	n, _ := strconv.Atoi(s)
	return n
}

func idFromPoints(points []PointDef) int {
	for _, p := range points {
		if p.Name == "ID" && p.Value != nil {
			switch v := p.Value.(type) {
			case float64:
				return int(v)
			case int:
				return v
			}
		}
	}
	return 0
}
