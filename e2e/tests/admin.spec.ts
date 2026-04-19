// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { AdminPage } from "../pages/admin.page";
import { TeacherPage } from "../pages/teacher.page";
import { ADMIN_ID, ADMIN_PASSWORD } from "../helpers/server";

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
