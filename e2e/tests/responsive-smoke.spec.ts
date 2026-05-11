// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { expect, test, type Page } from "@playwright/test";
import { loginAsAdmin, loginAsTeacher } from "../helpers/auth";
import {
  getSeedResult,
  TEACHER_ID,
  TEACHER_PASSWORD,
} from "../helpers/seed";
import { ADMIN_ID, ADMIN_PASSWORD } from "../helpers/server";
import { StudentPage } from "../pages/student.page";

async function expectNoDocumentOverflow(page: Page): Promise<void> {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    const body = document.body;
    const scrollWidth = Math.max(root.scrollWidth, body?.scrollWidth ?? 0);
    const clientWidth = root.clientWidth;
    return {
      scrollWidth,
      clientWidth,
      overflowBy: scrollWidth - clientWidth,
    };
  });

  expect(
    overflow.overflowBy,
    `document overflows horizontally by ${overflow.overflowBy}px`
  ).toBeLessThanOrEqual(2);
}

test.describe("responsive smoke", () => {
  test("student landing and course panel fit the viewport", async ({ page }) => {
    const seed = getSeedResult();
    const student = new StudentPage(page);

    await student.goto();
    await expect(student.landing).toBeVisible();
    await expectNoDocumentOverflow(page);

    await student.enterCode(seed.inviteCode);
    await expect(student.panel).toBeVisible();
    await expect(student.hdrCourseName).toContainText("E2E测试课程");
    await expectNoDocumentOverflow(page);

    await student.switchTab("tab-materials");
    await expect(student.materialsContent).toBeVisible();
    await expectNoDocumentOverflow(page);
  });

  test("teacher panel, docs, and admin panel fit the viewport", async ({ page }) => {
    await loginAsTeacher(page, TEACHER_ID, TEACHER_PASSWORD);
    await expect(page.locator("#view-main")).toBeVisible();
    await expect(page.locator("#coursePills")).toBeVisible();
    await expectNoDocumentOverflow(page);

    await page.locator("#agentLauncher").click();
    await expect(page.locator("#agentPanel")).toBeVisible();
    await expectNoDocumentOverflow(page);

    await page.goto("/teacher/docs");
    await expect(page.getByRole("heading", { name: "教师使用文档" })).toBeVisible();
    await expect(page.locator("#docContent")).toContainText("课程助手教师使用说明");
    await expectNoDocumentOverflow(page);

    await loginAsAdmin(page, ADMIN_ID, ADMIN_PASSWORD);
    await expect(page.locator("#view-main")).toBeVisible();
    await expect(page.locator(".tab-bar")).toBeVisible();
    await expectNoDocumentOverflow(page);
  });
});
