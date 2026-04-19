// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page, type Locator } from "@playwright/test";

export class ResultPage {
  readonly page: Page;
  readonly title: Locator;
  readonly list: Locator;
  readonly pdfBtn: Locator;
  readonly imgBtn: Locator;
  readonly retryBtn: Locator;
  readonly homeBtn: Locator;
  readonly aiBtn: Locator;
  readonly summary: Locator;

  constructor(page: Page) {
    this.page = page;
    this.title = page.locator("#title");
    this.list = page.locator("#list");
    this.pdfBtn = page.locator("#pdfBtn");
    this.imgBtn = page.locator("#imgBtn");
    this.retryBtn = page.locator("#retryBtn");
    this.homeBtn = page.locator("#homeBtn");
    this.aiBtn = page.locator("#aiBtn");
    this.summary = page.locator("#summary");
  }

  async waitForLoad() {
    await this.list.locator(".card").first().waitFor({
      state: "visible",
      timeout: 10_000,
    });
  }

  /** Returns title text, e.g. "第一周课堂反馈（3/3）" */
  async getTitle(): Promise<string> {
    return (await this.title.textContent()) ?? "";
  }

  /** Parse score from title like "第一周课堂反馈（3/3）" */
  async getScore(): Promise<{ correct: number; total: number }> {
    const text = await this.getTitle();
    // Match both full-width and half-width parentheses
    const match = text.match(/[（(](\d+)\/(\d+)[）)]/);
    if (!match) return { correct: 0, total: 0 };
    return { correct: parseInt(match[1]), total: parseInt(match[2]) };
  }

  /** Count correct and incorrect answers */
  async getQuestionResults(): Promise<{ ok: number; bad: number }> {
    const okCount = await this.list.locator(".ok").count();
    const badCount = await this.list.locator(".bad").count();
    return { ok: okCount, bad: badCount };
  }

  async retry() {
    this.page.once("dialog", (d) => d.accept());
    await this.retryBtn.click();
    await this.page.waitForURL("**/quiz", { timeout: 10_000 });
  }

  async goHome() {
    await this.homeBtn.click();
    await this.page.waitForURL("**/", { timeout: 5000 });
  }
}
