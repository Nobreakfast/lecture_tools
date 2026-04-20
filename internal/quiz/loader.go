package quiz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"course-assistant/internal/domain"

	"gopkg.in/yaml.v3"
)

func Parse(content []byte) (*domain.Quiz, error) {
	var q domain.Quiz
	if err := yaml.Unmarshal(content, &q); err != nil {
		return nil, fmt.Errorf("题库格式错误: %w", err)
	}
	if err := Validate(&q); err != nil {
		return nil, err
	}
	return &q, nil
}

func Validate(q *domain.Quiz) error {
	if strings.TrimSpace(q.QuizID) == "" {
		return errors.New("quiz_id 不能为空")
	}
	if len(q.Questions) == 0 {
		return errors.New("questions 不能为空")
	}
	seen := map[string]struct{}{}
	tagCount := map[string]int{}
	for i, item := range q.Questions {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("第 %d 题 id 不能为空", i+1)
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("题目 id 重复: %s", item.ID)
		}
		seen[item.ID] = struct{}{}
		switch item.Type {
		case domain.QuestionSingleChoice, domain.QuestionMultiChoice, domain.QuestionYesNo, domain.QuestionSurvey, domain.QuestionShortAnswer:
		default:
			return fmt.Errorf("题目 %s 类型无效: %s", item.ID, item.Type)
		}
		if item.Type != domain.QuestionSurvey && item.AllowMultiple {
			return fmt.Errorf("题目 %s 仅 survey 题型支持 allow_multiple", item.ID)
		}
		if strings.TrimSpace(item.Stem) == "" {
			return fmt.Errorf("题目 %s stem 不能为空", item.ID)
		}
		if item.Type == domain.QuestionShortAnswer {
			if len(item.Options) != 0 {
				return fmt.Errorf("题目 %s 简答题不能配置 options", item.ID)
			}
			if strings.TrimSpace(item.CorrectAnswer) != "" {
				return fmt.Errorf("题目 %s 简答题不能配置 correct_answer", item.ID)
			}
			mode := strings.TrimSpace(item.ShortAnswerMode)
			switch mode {
		case "", "text", "image", "code", "text_image":
		default:
			return fmt.Errorf("题目 %s short_answer_mode 无效: %s（仅支持 text/image/code/text_image）", item.ID, mode)
			}
			if tag := strings.TrimSpace(item.PoolTag); tag != "" {
				return fmt.Errorf("题目 %s 简答题不能配置 pool_tag", item.ID)
			}
			continue
		}
		if strings.TrimSpace(item.ShortAnswerMode) != "" {
			return fmt.Errorf("题目 %s 仅 short_answer 题型支持 short_answer_mode", item.ID)
		}
		if len(item.Options) < 2 {
			return fmt.Errorf("题目 %s options 至少 2 个", item.ID)
		}
		optSeen := map[string]struct{}{}
		for _, op := range item.Options {
			if strings.TrimSpace(op.Key) == "" {
				return fmt.Errorf("题目 %s 存在空选项", item.ID)
			}
			if strings.TrimSpace(op.Text) == "" && strings.TrimSpace(op.Image) == "" {
				return fmt.Errorf("题目 %s 选项需至少包含文本或图片", item.ID)
			}
			if _, ok := optSeen[op.Key]; ok {
				return fmt.Errorf("题目 %s 选项 key 重复: %s", item.ID, op.Key)
			}
			optSeen[op.Key] = struct{}{}
		}
		if item.Type != domain.QuestionSurvey {
			if item.Type == domain.QuestionMultiChoice {
				normalized, err := normalizeMultiAnswer(item.CorrectAnswer, optSeen)
				if err != nil {
					return fmt.Errorf("题目 %s %v", item.ID, err)
				}
				item.CorrectAnswer = normalized
				q.Questions[i].CorrectAnswer = normalized
			} else {
				if _, ok := optSeen[item.CorrectAnswer]; !ok {
					return fmt.Errorf("题目 %s correct_answer 未命中选项", item.ID)
				}
			}
		}
		if tag := strings.TrimSpace(item.PoolTag); tag != "" {
			tagCount[tag]++
		}
	}
	if q.Sampling != nil {
		if len(q.Sampling.Groups) == 0 {
			return errors.New("sampling.groups 不能为空")
		}
		seenTag := map[string]struct{}{}
		for _, group := range q.Sampling.Groups {
			tag := strings.TrimSpace(group.Tag)
			if tag == "" {
				return errors.New("sampling.groups.tag 不能为空")
			}
			if group.Pick <= 0 {
				return fmt.Errorf("sampling.groups[%s].pick 必须大于 0", tag)
			}
			if _, ok := seenTag[tag]; ok {
				return fmt.Errorf("sampling.groups.tag 重复: %s", tag)
			}
			seenTag[tag] = struct{}{}
			if tagCount[tag] < group.Pick {
				return fmt.Errorf("sampling.groups[%s] 题目不足: 需要 %d, 实际 %d", tag, group.Pick, tagCount[tag])
			}
		}
	}
	return nil
}

func ValidateImagePaths(q *domain.Quiz, baseDirs ...string) error {
	if len(baseDirs) == 0 {
		return errors.New("图片目录未配置")
	}
	candidates := make([]string, 0, len(baseDirs))
	for _, baseDir := range baseDirs {
		baseDir = strings.TrimSpace(baseDir)
		if baseDir == "" {
			continue
		}
		candidates = append(candidates, baseDir)
	}
	if len(candidates) == 0 {
		return errors.New("图片目录未配置")
	}
	for _, item := range q.Questions {
		if strings.TrimSpace(item.Image) == "" {
		} else {
			if !existsInBases(item.Image, candidates) {
				return fmt.Errorf("题目 %s 图片不存在: %s", item.ID, item.Image)
			}
		}
		for _, op := range item.Options {
			if strings.TrimSpace(op.Image) == "" {
				continue
			}
			if !existsInBases(op.Image, candidates) {
				return fmt.Errorf("题目 %s 选项 %s 图片不存在: %s", item.ID, op.Key, op.Image)
			}
		}
	}
	return nil
}

func existsInBases(name string, baseDirs []string) bool {
	for _, baseDir := range baseDirs {
		target := filepath.Join(baseDir, name)
		if _, err := os.Stat(target); err == nil {
			return true
		}
	}
	return false
}

func normalizeMultiAnswer(raw string, options map[string]struct{}) (string, error) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return "", errors.New("correct_answer 至少包含一个选项")
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		key := strings.TrimSpace(p)
		if key == "" {
			continue
		}
		if _, ok := options[key]; !ok {
			return "", errors.New("correct_answer 未命中选项")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return "", errors.New("correct_answer 至少包含一个选项")
	}
	sort.Strings(out)
	return strings.Join(out, ","), nil
}
