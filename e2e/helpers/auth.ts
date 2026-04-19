// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page } from "@playwright/test";

export async function loginAsTeacher(
  page: Page,
  id: string,
  password: string
): Promise<void> {
  await page.goto("/teacher");
  await page.locator("#loginId").fill(id);
  await page.locator("#loginPwd").fill(password);
  await page.locator("#loginBtn").click();
  await page.locator("#view-main").waitFor({ state: "visible" });
}

export async function loginAsAdmin(
  page: Page,
  id: string,
  password: string
): Promise<void> {
  await page.goto("/admin");
  await page.locator("#loginId").fill(id);
  await page.locator("#loginPwd").fill(password);
  await page.locator("#loginBtn").click();
  await page.locator("#view-main").waitFor({ state: "visible" });
}
