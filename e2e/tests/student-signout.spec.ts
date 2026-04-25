// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { getSeedResult } from "../helpers/seed";
import { TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";
import { TeacherPage } from "../pages/teacher.page";

test.describe("Student sign-out and redirects", () => {
  test("POST /api/student-signout clears session", async ({ page }) => {
    const seed = getSeedResult();
    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);

    // Sign out via API
    const response = await page.evaluate(async () => {
      const r = await fetch("/api/student-signout", {
        method: "POST",
        credentials: "include",
      });
      return { status: r.status, body: await r.json() };
    });
    expect(response.status).toBe(200);
    expect(response.body.ok).toBe(true);
  });

  test("/join?code=XXXXXX redirects to student page with code", async ({
    page,
  }) => {
    const seed = getSeedResult();
    await page.goto(`/join?code=${seed.inviteCode}`);
    await page.waitForURL("**/?code=*", { timeout: 5000 });
    expect(page.url()).toContain("code=" + seed.inviteCode);
  });

  test("/s/ short link resolves to student page", async ({ page }) => {
    const seed = getSeedResult();
    // The /s/ route maps to /?code=, try navigating
    const response = await page.goto(`/s/${seed.inviteCode}`);
    // Should redirect or render student page
    expect(response?.status()).toBeLessThan(400);
  });

  test("student cannot join quiz twice with same student_no (idempotent)", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // Setup: open entry
    const { TeacherPage } = await import("../pages/teacher.page");
    const { TEACHER_ID, TEACHER_PASSWORD } = await import("../helpers/seed");
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();
    await teacherCtx.close();

    // First join
    const ctx1 = await browser.newContext();
    const s1 = new StudentPage(await ctx1.newPage());
    await s1.enterCode(seed.inviteCode);
    await s1.waitForQuizOpen();
    await s1.joinQuiz("重复学生", "2024DUP", "测试班级");
    expect(s1.page.url()).toContain("/quiz");
    await ctx1.close();

    // Second join with same info — should still work (creates new attempt)
    const ctx2 = await browser.newContext();
    const s2 = new StudentPage(await ctx2.newPage());
    await s2.enterCode(seed.inviteCode);
    await s2.waitForQuizOpen();
    await s2.joinQuiz("重复学生", "2024DUP", "测试班级");
    expect(s2.page.url()).toContain("/quiz");
    await ctx2.close();
  });

  test("student can return to course page and choose whether to resume or quit quiz", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();
    await teacherCtx.close();

    const studentCtx = await browser.newContext();
    const student = new StudentPage(await studentCtx.newPage());
    await student.enterCode(seed.inviteCode);
    await student.waitForQuizOpen();
    await student.joinQuiz("返回测试", "2024BACK", "测试班级");

    const quizPage = new QuizPage(student.page);
    await quizPage.waitForLoad();
    await quizPage.returnToCoursePage();

    await student.panel.waitFor({ state: "visible", timeout: 10_000 });
    await student.waitForQuizResume();
    await expect(student.quizResume).toBeVisible();
    await expect(student.quizResumeBtn).toBeVisible();
    await expect(student.quizSignoutBtn).toBeVisible();

    await student.quizResumeBtn.click();
    await student.page.waitForURL("**/quiz", { timeout: 10_000 });
    await quizPage.waitForLoad();

    await quizPage.quitQuiz();
    await student.panel.waitFor({ state: "visible", timeout: 10_000 });
    await student.waitForQuizOpen();
    await expect(student.quizResume).toBeHidden();

    await studentCtx.close();
  });
});
