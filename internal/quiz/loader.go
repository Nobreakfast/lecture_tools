package quiz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		case domain.QuestionSingleChoice, domain.QuestionYesNo, domain.QuestionSurvey, domain.QuestionShortAnswer:
		default:
			return fmt.Errorf("题目 %s 类型无效: %s", item.ID, item.Type)
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
			if tag := strings.TrimSpace(item.PoolTag); tag != "" {
				return fmt.Errorf("题目 %s 简答题不能配置 pool_tag", item.ID)
			}
			continue
		}
		if len(item.Options) < 2 {
			return fmt.Errorf("题目 %s options 至少 2 个", item.ID)
		}
		optSeen := map[string]struct{}{}
		for _, op := range item.Options {
			if strings.TrimSpace(op.Key) == "" || strings.TrimSpace(op.Text) == "" {
				return fmt.Errorf("题目 %s 存在空选项", item.ID)
			}
			if _, ok := optSeen[op.Key]; ok {
				return fmt.Errorf("题目 %s 选项 key 重复: %s", item.ID, op.Key)
			}
			optSeen[op.Key] = struct{}{}
		}
		if item.Type != domain.QuestionSurvey {
			if _, ok := optSeen[item.CorrectAnswer]; !ok {
				return fmt.Errorf("题目 %s correct_answer 未命中选项", item.ID)
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

func ValidateImagePaths(q *domain.Quiz, baseDir string) error {
	for _, item := range q.Questions {
		if strings.TrimSpace(item.Image) == "" {
			continue
		}
		target := filepath.Join(baseDir, item.Image)
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("题目 %s 图片不存在: %s", item.ID, item.Image)
		}
	}
	return nil
}
