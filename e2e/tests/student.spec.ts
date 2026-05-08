// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { StudentPage } from "../pages/student.page";
import { TeacherPage } from "../pages/teacher.page";
import {
  getSeedResult,
  TEACHER_B_ID,
  TEACHER_B_PASSWORD,
  TEACHER_ID,
  TEACHER_PASSWORD,
} from "../helpers/seed";

test.describe("Student landing and course panel", () => {
  test("enter valid invite code → see course panel", async ({ page }) => {
    const seed = getSeedResult();
    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);

    await expect(student.panel).toBeVisible();
    await expect(student.hdrCourseName).toContainText("E2E测试课程");
  });

  test("invalid invite code shows error", async ({ page }) => {
    const student = new StudentPage(page);
    await student.goto();
    await student.codeInput.fill("ZZZZZZ");
    await student.codeBtn.click();
    await expect(student.codeError).not.toBeEmpty();
  });

  test("quiz tab shows waiting when entry is closed", async ({
    page,
    browser,
  }) => {
    const seed = getSeedResult();

    // Ensure entry is closed via teacher
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.closeEntry();
    await teacherCtx.close();

    // Student sees waiting state
    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);
    await student.switchTab("tab-quiz");
    await expect(student.quizWait).toBeVisible({ timeout: 10_000 });
  });

  test("quiz tab shows join form when entry is open", async ({
    page,
    browser,
  }) => {
    const seed = getSeedResult();

    // Open entry via teacher
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();
    await teacherCtx.close();

    // Student sees join form
    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);
    await student.waitForQuizOpen();
    await expect(student.quizForm).toBeVisible();
    await expect(student.quizTitle).toContainText("第一周课堂反馈");
  });

  test("join quiz redirects to /quiz", async ({ page, browser }) => {
    const seed = getSeedResult();

    // Open entry via teacher
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();
    await teacherCtx.close();

    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);
    await student.waitForQuizOpen();
    await student.joinQuiz("学生测试", "2024099", "自动化班");
    expect(page.url()).toContain("/quiz");
  });

  test("QR code course overrides a stale session from another course", async ({
    page,
    browser,
  }) => {
    const seed = getSeedResult();

    const teacherACtx = await browser.newContext();
    const teacherA = new TeacherPage(await teacherACtx.newPage());
    await teacherA.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherA.selectCourse(seed.courseId);
    await teacherA.uploadQuizYAML();
    await teacherA.openEntry();
    await teacherACtx.close();

    const teacherBCtx = await browser.newContext();
    const teacherB = new TeacherPage(await teacherBCtx.newPage());
    await teacherB.login(TEACHER_B_ID, TEACHER_B_PASSWORD);
    await teacherB.selectCourse(seed.courseIdB);
    await teacherB.uploadQuizYAML();
    await teacherB.openEntry();
    await teacherBCtx.close();

    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCodeB);
    await student.waitForQuizOpen();
    await student.joinQuiz("跨课扫码学生", "QR-SWITCH-001", "自动化班");
    expect(page.url()).toContain("/quiz");

    await page.goto(`/?code=${seed.inviteCode}`);
    await student.panel.waitFor({ state: "visible", timeout: 10_000 });
    await expect(student.hdrCourseName).toContainText("E2E测试课程");
    await expect(student.hdrCourseName).not.toContainText("E2E测试课程B");
    await expect(page).not.toHaveURL(/\/quiz$/);
  });

  test("materials tab is accessible", async ({ page }) => {
    const seed = getSeedResult();
    const student = new StudentPage(page);
    await student.enterCode(seed.inviteCode);
    await student.switchTab("tab-materials");
    await expect(student.materialsContent).toBeVisible();
  });
});
