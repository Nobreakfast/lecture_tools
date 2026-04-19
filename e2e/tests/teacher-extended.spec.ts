// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Teacher extended features", () => {
  let teacherPage: TeacherPage;

  test.beforeEach(async ({ page }) => {
    teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
  });

  test("login with wrong password shows error", async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const fresh = new TeacherPage(page);
    await fresh.goto();
    await fresh.loginId.fill("nonexistent");
    await fresh.loginPwd.fill("wrongpwd");
    await fresh.loginBtn.click();
    await page.waitForTimeout(500);
    // Should remain on login view (view-main not visible)
    await expect(fresh.viewMain).not.toBeVisible();
    await ctx.close();
  });

  test("export CSV downloads a file", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.switchTab("tab-attempts");

    const downloadPromise = teacherPage.page.waitForEvent("download");
    await teacherPage.exportCsvBtn.click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/\.csv$/i);
  });

  test("view attempt detail shows student info", async ({ browser }) => {
    const seed = getSeedResult();

    // Create an attempt first
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.waitForQuizOpen();
    await studentPage.joinQuiz("详情学生", "2024D01", "详情班");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "查看详情",
    });
    await quiz.submit();
    await studentCtx.close();

    // Reload teacher page to see the attempt
    await teacherPage.page.reload();
    await teacherPage.viewMain.waitFor({ state: "visible" });
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.switchTab("tab-attempts");
    await teacherPage.page.waitForTimeout(1000);

    await expect(
      teacherPage.attemptsList.locator("text=详情学生")
    ).toBeVisible({ timeout: 5000 });

    await teacherPage.viewAttemptDetail("详情学生");

    await expect(teacherPage.detailTitle).toContainText("详情学生");
    await expect(teacherPage.detailContent).toContainText(
      "Go 中用于定义结构体的关键字是"
    );

    await teacherPage.closeDetailModal();
  });

  test("delete course removes it from list", async () => {
    // Create a temporary course
    const slug = `del-course-${Date.now()}`;
    await teacherPage.createCourse("待删课程", slug);
    await expect(teacherPage.courseList).toContainText("待删课程");

    // Get the course ID from the DOM
    const courseCard = teacherPage.courseList.locator(".course-card", {
      has: teacherPage.page.locator("text=待删课程"),
    });
    const cardId = await courseCard.getAttribute("id");
    const courseId = cardId?.replace("courseCard_", "") ?? "";

    await teacherPage.deleteCourse(Number(courseId), "待删课程");

    await expect(teacherPage.courseList).not.toContainText("待删课程");
  });

  test("invite QR code downloads a PNG", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    const filename = await teacherPage.downloadInviteQR(seed.courseId);
    expect(filename).toMatch(/\.png$/i);
  });
});
