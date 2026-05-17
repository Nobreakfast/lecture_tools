// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { AdminPage } from "../pages/admin.page";
import { TeacherPage } from "../pages/teacher.page";
import { ADMIN_ID, ADMIN_PASSWORD } from "../helpers/server";
import { getSeedResult } from "../helpers/seed";

test.describe("Admin panel", () => {
  let adminPage: AdminPage;

  test.beforeEach(async ({ page }) => {
    adminPage = new AdminPage(page);
    await adminPage.login(ADMIN_ID, ADMIN_PASSWORD);
  });

  test("login shows overview with stats", async () => {
    await expect(adminPage.viewMain).toBeVisible();
    const stats = await adminPage.getOverviewStats();
    // At least the seeded admin + teacher should exist
    expect(parseInt(stats.teachers ?? "0")).toBeGreaterThanOrEqual(2);
    expect(parseInt(stats.courses ?? "0")).toBeGreaterThanOrEqual(1);
    await expect(adminPage.onlineStudentTbody).toBeVisible();
    await expect(adminPage.onlineTeacherTbody).toBeVisible();
    await expect(adminPage.recentLoginTbody).toBeVisible();
  });

  test("create teacher and see in list", async () => {
    const id = `test_t_${Date.now()}`;
    await adminPage.createTeacher(id, "临时教师", "temppass");
    const ids = await adminPage.getTeacherRows();
    expect(ids).toContain(id);
  });

  test("reset teacher password and verify login", async ({ browser }) => {
    const id = `reset_t_${Date.now()}`;
    const newPwd = "resetpwd123";
    await adminPage.createTeacher(id, "重置测试", "oldpwd");
    await adminPage.resetTeacherPassword(id, newPwd);

    // Verify: teacher can login with new password
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const teacherPage = new TeacherPage(page);
    await teacherPage.login(id, newPwd);
    await expect(teacherPage.viewMain).toBeVisible();
    await ctx.close();
  });

  test("teacher login appears in recent login list", async ({ browser }) => {
    const id = `login_t_${Date.now()}`;
    const pwd = "recentpwd123";
    await adminPage.createTeacher(id, "最近登录教师", pwd);

    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const teacherPage = new TeacherPage(page);
    await teacherPage.login(id, pwd);
    await ctx.close();

    await adminPage.switchTab("overview");
    await expect(adminPage.recentLoginTbody).toContainText(id);
    await expect(adminPage.onlineTeacherTbody).toContainText(id);
  });

  test("student quiz login appears in online and recent login lists", async () => {
    const seed = getSeedResult();
    const openResp = await adminPage.page.request.post(
      `/api/teacher/courses/entry?course_id=${seed.courseId}`,
      {
        headers: { Cookie: seed.teacherCookie },
        data: { open: true },
      }
    );
    expect(openResp.ok()).toBeTruthy();

    const studentNo = `S${Date.now()}`;
    const joinResp = await adminPage.page.request.post("/api/join", {
      data: {
        course_id: seed.courseId,
        name: "在线学生",
        student_no: studentNo,
        class_name: "E2E班",
      },
    });
    expect(joinResp.ok()).toBeTruthy();

    await adminPage.switchTab("overview");
    await expect(adminPage.onlineStudentTbody).toContainText(studentNo);
    await expect(adminPage.recentLoginTbody).toContainText(studentNo);
  });

  test("delete teacher removes from list", async () => {
    const id = `del_t_${Date.now()}`;
    await adminPage.createTeacher(id, "删除测试", "delpwd");
    let ids = await adminPage.getTeacherRows();
    expect(ids).toContain(id);

    await adminPage.deleteTeacher(id);
    ids = await adminPage.getTeacherRows();
    expect(ids).not.toContain(id);
  });

  test("AI health check shows result", async () => {
    await adminPage.switchTab("ai");
    await adminPage.aiHealthBtn.click();
    await adminPage.page.waitForTimeout(1000);
    const result = await adminPage.aiHealthResult.textContent();
    // Without AI configured, it should show an error/status (not "未测试")
    expect(result).not.toBe("未测试");
  });
});
