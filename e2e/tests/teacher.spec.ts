// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { TEACHER_ID, TEACHER_PASSWORD, getSeedResult } from "../helpers/seed";

test.describe("Teacher panel", () => {
  let teacherPage: TeacherPage;

  test.beforeEach(async ({ page }) => {
    teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
  });

  test("login shows teacher name and main view", async () => {
    await expect(teacherPage.viewMain).toBeVisible();
    await expect(teacherPage.teacherName).not.toBeEmpty();
  });

  test("seeded course appears in course list", async () => {
    await teacherPage.switchTab("tab-courses");
    await expect(teacherPage.courseList).toContainText("E2E测试课程");
  });

  test("create a new course", async () => {
    const slug = `e2e-new-${Date.now()}`;
    await teacherPage.createCourse("新建测试课程", slug);
    await expect(teacherPage.courseList).toContainText("新建测试课程");
  });

  test("upload quiz YAML and see title", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.switchTab("tab-attempts");
    await expect(teacherPage.quizTitle).toContainText("第一周课堂反馈");
  });

  test("toggle entry open and closed", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();

    await teacherPage.openEntry();
    let status = await teacherPage.getEntryStatusText();
    expect(status).toContain("开放");

    await teacherPage.closeEntry();
    status = await teacherPage.getEntryStatusText();
    expect(status).toContain("关闭");
  });

  test("attempts tab shows empty state initially", async () => {
    // Create a fresh course so there are no attempts
    const slug = `e2e-empty-${Date.now()}`;
    await teacherPage.createCourse("空课程", slug);
    // Need to select the new course — get its ID from the dropdown
    await teacherPage.page.waitForTimeout(500);
    await teacherPage.switchTab("tab-attempts");
    // The quiz title should show the default placeholder
    await expect(teacherPage.quizTitle).toHaveText("-");
  });

  test("change password", async () => {
    const newPassword = `newpwd-${Date.now()}`;
    await teacherPage.changePassword(TEACHER_PASSWORD, newPassword);

    // Verify: logout and login with new password
    await teacherPage.page.locator("text=退出登录").click();
    await teacherPage.page.waitForTimeout(500);

    const freshPage = new TeacherPage(teacherPage.page);
    await freshPage.login(TEACHER_ID, newPassword);
    await expect(freshPage.viewMain).toBeVisible();

    // Restore original password for other tests
    await freshPage.changePassword(newPassword, TEACHER_PASSWORD);
  });
});
