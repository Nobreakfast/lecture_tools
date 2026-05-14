// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func unmarshalAIJSONObject(content string, dst any) error {
	content = strings.TrimSpace(stripCodeFence(content))
	if content == "" {
		return fmt.Errorf("AI 返回为空，无法解析 JSON")
	}

	var firstErr error
	for _, candidate := range aiJSONCandidates(content) {
		resetJSONTarget(dst)
		decoder := json.NewDecoder(strings.NewReader(candidate))
		if err := decoder.Decode(dst); err == nil {
			return nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	resetJSONTarget(dst)
	return firstErr
}

func aiJSONCandidates(content string) []string {
	candidates := []string{content}
	seen := map[string]bool{content: true}
	for i, r := range content {
		if r != '{' {
			continue
		}
		candidate := strings.TrimSpace(content[i:])
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	return candidates
}

func resetJSONTarget(dst any) {
	v := reflect.ValueOf(dst)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	elem := v.Elem()
	if elem.CanSet() {
		elem.Set(reflect.Zero(elem.Type()))
	}
}
