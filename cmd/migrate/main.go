package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := "./data/app.db"
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("数据库文件不存在: %s\n", dbPath)
		fmt.Println("如果是新部署，直接启动服务即可，无需迁移。")
		os.Exit(0)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. 启用 WAL 模式
	fmt.Println("=== 步骤 1: 启用 WAL 模式 ===")
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		log.Fatalf("WAL 模式启用失败: %v", err)
	}
	fmt.Println("✓ WAL 模式已启用")

	// 2. 检查重复的 in_progress 记录
	fmt.Println("\n=== 步骤 2: 检查重复的 in_progress 记录 ===")
	rows, err := db.QueryContext(ctx, `
		SELECT quiz_id, student_no, COUNT(*) as cnt,
		       GROUP_CONCAT(id, ', ') as ids
		FROM attempts
		WHERE status = 'in_progress'
		GROUP BY quiz_id, student_no
		HAVING cnt > 1
	`)
	if err != nil {
		log.Fatalf("查询失败: %v", err)
	}

	type duplicate struct {
		quizID    string
		studentNo string
		count     int
		ids       string
	}
	var dups []duplicate
	for rows.Next() {
		var d duplicate
		if err := rows.Scan(&d.quizID, &d.studentNo, &d.count, &d.ids); err != nil {
			log.Fatalf("读取行失败: %v", err)
		}
		dups = append(dups, d)
	}
	rows.Close()

	if len(dups) == 0 {
		fmt.Println("✓ 没有重复记录，无需清理")
	} else {
		fmt.Printf("⚠ 发现 %d 组重复记录：\n", len(dups))
		for _, d := range dups {
			fmt.Printf("  quiz=%s student_no=%s 数量=%d ids=[%s]\n", d.quizID, d.studentNo, d.count, d.ids)
		}

		// 3. 清理重复：保留最新的一条，其余标记为 submitted（attempt_no=0 表示未正式提交）
		fmt.Println("\n=== 步骤 3: 清理重复记录 ===")
		fmt.Println("策略: 每组保留最新的 in_progress，其余改为 submitted (attempt_no=0)")

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Fatalf("事务启动失败: %v", err)
		}

		now := time.Now().Format(time.RFC3339Nano)
		totalCleaned := 0
		for _, d := range dups {
			// 找出该组最新的一条
			var keepID string
			err := tx.QueryRowContext(ctx, `
				SELECT id FROM attempts
				WHERE quiz_id = ? AND student_no = ? AND status = 'in_progress'
				ORDER BY created_at DESC LIMIT 1
			`, d.quizID, d.studentNo).Scan(&keepID)
			if err != nil {
				_ = tx.Rollback()
				log.Fatalf("查找最新记录失败: %v", err)
			}

			// 把其余的标记为 submitted
			res, err := tx.ExecContext(ctx, `
				UPDATE attempts
				SET status = 'submitted', attempt_no = 0, updated_at = ?, submitted_at = ?
				WHERE quiz_id = ? AND student_no = ? AND status = 'in_progress' AND id != ?
			`, now, now, d.quizID, d.studentNo, keepID)
			if err != nil {
				_ = tx.Rollback()
				log.Fatalf("更新失败: %v", err)
			}
			affected, _ := res.RowsAffected()
			totalCleaned += int(affected)
			fmt.Printf("  ✓ student_no=%s: 保留 %s，清理 %d 条\n", d.studentNo, keepID, affected)
		}

		if err := tx.Commit(); err != nil {
			log.Fatalf("事务提交失败: %v", err)
		}
		fmt.Printf("✓ 共清理 %d 条重复记录\n", totalCleaned)
	}

	// 4. 创建索引
	fmt.Println("\n=== 步骤 4: 创建索引 ===")
	indexes := []struct {
		name string
		sql  string
	}{
		{"idx_attempts_quiz_status", `CREATE INDEX IF NOT EXISTS idx_attempts_quiz_status ON attempts(quiz_id, status)`},
		{"idx_attempts_lookup", `CREATE INDEX IF NOT EXISTS idx_attempts_lookup ON attempts(quiz_id, student_no, status)`},
		{"idx_answers_attempt", `CREATE INDEX IF NOT EXISTS idx_answers_attempt ON answers(attempt_id)`},
		{"idx_attempts_one_active", `CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_one_active ON attempts(quiz_id, student_no) WHERE status = 'in_progress'`},
	}
	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx.sql); err != nil {
			log.Fatalf("创建索引 %s 失败: %v", idx.name, err)
		}
		fmt.Printf("✓ %s\n", idx.name)
	}

	// 5. 统计
	fmt.Println("\n=== 迁移完成 ===")
	var total, inProgress, submitted int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts`).Scan(&total)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE status = 'in_progress'`).Scan(&inProgress)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE status = 'submitted'`).Scan(&submitted)
	fmt.Printf("当前数据: 总记录=%d, 进行中=%d, 已提交=%d\n", total, inProgress, submitted)
	fmt.Println("✓ 可以安全启动新版本服务")
}
