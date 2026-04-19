// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Multi-user scenarios", () => {
  test("two students answer concurrently and teacher sees both", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher: setup ──
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    // ── Student A ──
    const ctxA = await browser.newContext();
    const studentA = new StudentPage(await ctxA.newPage());
    await studentA.enterCode(seed.inviteCode);
    await studentA.waitForQuizOpen();
    await studentA.joinQuiz("学生A", "A001", "并发班");

    // ── Student B ──
    const ctxB = await browser.newContext();
    const studentB = new StudentPage(await ctxB.newPage());
    await studentB.enterCode(seed.inviteCode);
    await studentB.waitForQuizOpen();
    await studentB.joinQuiz("学生B", "B002", "并发班");

    // ── Both answer and submit (sequentially to avoid SQLite write contention) ──
    const answerAndSubmit = async (studentPage: StudentPage) => {
      const quiz = new QuizPage(studentPage.page);
      await quiz.waitForLoad();
      await quiz.answerAllViaAPI({
        q1: "B",
        q2: "Y",
        q3: "A,B,C",
        q4: "A",
        q5: "并发测试回答",
      });
      await quiz.submit();

      const result = new ResultPage(studentPage.page);
      await result.waitForLoad();
      return result.getScore();
    };

    const scoreA = await answerAndSubmit(studentA);
    const scoreB = await answerAndSubmit(studentB);

    expect(scoreA.correct).toBe(3);
    expect(scoreB.correct).toBe(3);

    // ── Teacher: verify both attempts ──
    await teacherPage.page.reload();
    await teacherPage.viewMain.waitFor({ state: "visible" });
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.page.waitForTimeout(1500);

    await expect(
      teacherPage.attemptsList.locator("text=学生A")
    ).toBeVisible({ timeout: 5000 });
    await expect(
      teacherPage.attemptsList.locator("text=学生B")
    ).toBeVisible({ timeout: 5000 });

    // Cleanup
    await teacherCtx.close();
    await ctxA.close();
    await ctxB.close();
  });

  test("teacher SSE live stats update when student joins and submits", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher: setup and watch live stats ──
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    // Clear previous attempts first
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.clearAttemptsBtn.click();
    // Handle the built-in #confirmModal
    const confirmModal = teacherPage.page.locator("#confirmModal");
    await confirmModal.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    if (await confirmModal.isVisible()) {
      await teacherPage.confirmOkBtn.click();
      await teacherPage.page.waitForTimeout(500);
    }
    await teacherPage.openEntry();

    // Wait for SSE to establish
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.page.waitForTimeout(2000);

    // Verify initial state
    const initialStarted = await teacherPage.liveStarted.textContent();
    expect(initialStarted?.trim()).toBe("0");

    // ── Student joins ──
    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.waitForQuizOpen();
    await studentPage.joinQuiz("SSE学生", "SSE001", "实时班");

    // ── Teacher: wait for liveStarted to increment (SSE ~2s) ──
    await expect(teacherPage.liveStarted).not.toHaveText("0", {
      timeout: 10_000,
    });

    // ── Student submits ──
    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "SSE测试",
    });
    await quiz.submit();

    // ── Teacher: wait for liveSubmitted to increment ──
    await expect(teacherPage.liveSubmitted).not.toHaveText("0", {
      timeout: 10_000,
    });

    // Cleanup
    await teacherCtx.close();
    await studentCtx.close();
  });

  test("student cannot join quiz when entry is closed", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher: ensure entry is closed ──
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.closeEntry();
    await teacherCtx.close();

    // ── Student: should see waiting, not the join form ──
    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.switchTab("tab-quiz");

    // quizWait should be visible, quizForm should not
    await expect(studentPage.quizWait).toBeVisible({ timeout: 10_000 });
    await expect(studentPage.quizForm).not.toBeVisible();

    await studentCtx.close();
  });
});
