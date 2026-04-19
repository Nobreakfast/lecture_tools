// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { AdminPage } from "../pages/admin.page";
import { ADMIN_ID, ADMIN_PASSWORD } from "../helpers/server";

test.describe("Admin AI configuration", () => {
  let adminPage: AdminPage;

  test.beforeEach(async ({ page }) => {
    adminPage = new AdminPage(page);
    await adminPage.login(ADMIN_ID, ADMIN_PASSWORD);
  });

  test("save and reload AI config", async () => {
    const testEndpoint = "https://test.openai.example.com/v1";
    const testModel = "gpt-4o-test";

    await adminPage.saveAIConfig(testEndpoint, testModel);

    // Reload config
    const config = await adminPage.getAIConfig();
    expect(config.endpoint).toBe(testEndpoint);
    expect(config.model).toBe(testModel);
  });

  test("AI health check reflects config state", async () => {
    // Save a dummy config
    await adminPage.saveAIConfig(
      "https://dummy.example.com/v1",
      "test-model",
      "sk-test-key"
    );

    await adminPage.switchTab("ai");
    await adminPage.aiHealthBtn.click();
    await adminPage.page.waitForTimeout(2000);

    const result = await adminPage.aiHealthResult.textContent();
    // Should show some status (not the initial "未测试")
    expect(result).not.toBe("未测试");
    expect(result).not.toBe("检测中…");
  });

  test("admin login with wrong password stays on login", async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const freshAdmin = new AdminPage(page);
    await freshAdmin.goto();
    await freshAdmin.loginId.fill("wrong_admin");
    await freshAdmin.loginPwd.fill("wrong_password");
    await freshAdmin.loginBtn.click();
    await page.waitForTimeout(1000);
    // Should remain on login view
    await expect(freshAdmin.viewMain).not.toBeVisible();
    await ctx.close();
  });

  test("non-admin teacher cannot access admin panel", async ({ browser }) => {
    // Create a non-admin teacher via admin API, then try to login to admin page
    const id = `nonadmin_${Date.now()}`;
    await adminPage.createTeacher(id, "非管理员", "testpwd");

    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const freshAdmin = new AdminPage(page);
    await freshAdmin.goto();
    await freshAdmin.loginId.fill(id);
    await freshAdmin.loginPwd.fill("testpwd");
    await freshAdmin.loginBtn.click();
    await page.waitForTimeout(1000);

    // Admin panel checks role=admin; non-admin should stay on login
    await expect(freshAdmin.viewMain).not.toBeVisible();

    await ctx.close();
  });

  test("overview stats update after creating teacher", async () => {
    const statsBefore = await adminPage.getOverviewStats();
    const countBefore = parseInt(statsBefore.teachers ?? "0");

    const id = `stats_t_${Date.now()}`;
    await adminPage.createTeacher(id, "统计测试", "statspwd");

    // Refresh overview
    await adminPage.switchTab("overview");
    await adminPage.page.waitForTimeout(500);
    const statsAfter = await adminPage.getOverviewStats();
    const countAfter = parseInt(statsAfter.teachers ?? "0");

    expect(countAfter).toBeGreaterThan(countBefore);

    // Cleanup
    await adminPage.deleteTeacher(id);
  });
});
