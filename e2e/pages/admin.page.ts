// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page, type Locator, expect } from "@playwright/test";

export class AdminPage {
  readonly page: Page;
  readonly loginId: Locator;
  readonly loginPwd: Locator;
  readonly loginBtn: Locator;
  readonly viewMain: Locator;
  readonly logoutBtn: Locator;
  readonly whoami: Locator;

  // Overview tab
  readonly numTeachers: Locator;
  readonly numCourses: Locator;
  readonly numStudents: Locator;
  readonly numAttempts: Locator;

  // Teachers tab
  readonly newTeacherId: Locator;
  readonly newTeacherName: Locator;
  readonly newTeacherPwd: Locator;
  readonly createTeacherBtn: Locator;
  readonly teacherTbody: Locator;

  // AI tab
  readonly aiHealthBtn: Locator;
  readonly aiHealthResult: Locator;
  readonly aiEndpoint: Locator;
  readonly aiModel: Locator;
  readonly aiKey: Locator;
  readonly aiSaveBtn: Locator;
  readonly aiLoadBtn: Locator;

  constructor(page: Page) {
    this.page = page;
    this.loginId = page.locator("#loginId");
    this.loginPwd = page.locator("#loginPwd");
    this.loginBtn = page.locator("#loginBtn");
    this.viewMain = page.locator("#view-main");
    this.logoutBtn = page.locator("#logoutBtn");
    this.whoami = page.locator("#whoami");

    this.numTeachers = page.locator("#numTeachers");
    this.numCourses = page.locator("#numCourses");
    this.numStudents = page.locator("#numStudents");
    this.numAttempts = page.locator("#numAttempts");

    this.newTeacherId = page.locator("#newTeacherId");
    this.newTeacherName = page.locator("#newTeacherName");
    this.newTeacherPwd = page.locator("#newTeacherPwd");
    this.createTeacherBtn = page.locator("#createTeacherBtn");
    this.teacherTbody = page.locator("#teacherTbody");

    this.aiHealthBtn = page.locator("#aiHealthBtn");
    this.aiHealthResult = page.locator("#aiHealthResult");
    this.aiEndpoint = page.locator("#aiEndpoint");
    this.aiModel = page.locator("#aiModel");
    this.aiKey = page.locator("#aiKey");
    this.aiSaveBtn = page.locator("#aiSaveBtn");
    this.aiLoadBtn = page.locator("#aiLoadBtn");
  }

  async goto() {
    await this.page.goto("/admin");
  }

  async login(id: string, password: string) {
    await this.goto();
    await this.loginId.fill(id);
    await this.loginPwd.fill(password);
    await this.loginBtn.click();
    await this.viewMain.waitFor({ state: "visible" });
  }

  async switchTab(tab: "overview" | "teachers" | "ai") {
    await this.page.locator(`.tab-btn[data-tab="${tab}"]`).click();
    await this.page
      .locator(`.tab-page[data-tab="${tab}"]`)
      .waitFor({ state: "visible" });
  }

  async getOverviewStats() {
    await this.switchTab("overview");
    return {
      teachers: await this.numTeachers.textContent(),
      courses: await this.numCourses.textContent(),
      students: await this.numStudents.textContent(),
      attempts: await this.numAttempts.textContent(),
    };
  }

  async createTeacher(id: string, name: string, password: string) {
    await this.switchTab("teachers");
    await this.newTeacherId.fill(id);
    await this.newTeacherName.fill(name);
    await this.newTeacherPwd.fill(password);
    await this.createTeacherBtn.click();
    await this.page.waitForTimeout(500);
  }

  async getTeacherRows(): Promise<string[]> {
    await this.switchTab("teachers");
    await this.page.waitForTimeout(300);
    const rows = this.teacherTbody.locator("tr");
    const count = await rows.count();
    const ids: string[] = [];
    for (let i = 0; i < count; i++) {
      const text = await rows.nth(i).locator("td").first().textContent();
      if (text) ids.push(text.trim());
    }
    return ids;
  }

  async deleteTeacher(id: string) {
    await this.switchTab("teachers");
    const row = this.teacherTbody.locator(`tr`, {
      has: this.page.locator(`td`, { hasText: id }),
    });
    await row.locator('[data-act="delete"]').click();
    // App.confirmModal creates a .modal-backdrop > .modal with 确定 button
    const backdrop = this.page.locator(".modal-backdrop");
    await backdrop.waitFor({ state: "visible" });
    await backdrop.locator(".modal-actions .btn:not(.btn-secondary)").click();
    await this.page.waitForTimeout(500);
  }

  async resetTeacherPassword(id: string, newPassword = "") {
    await this.switchTab("teachers");
    const row = this.teacherTbody.locator(`tr`, {
      has: this.page.locator(`td`, { hasText: id }),
    });
    await row.locator('[data-act="reset"]').click();
    const backdrop = this.page.locator(".modal-backdrop");
    await backdrop.waitFor({ state: "visible" });
    if (newPassword) {
      await backdrop.locator("input[type=text]").fill(newPassword);
    }
    await backdrop.locator(".modal-actions .btn:not(.btn-secondary)").click();
    await this.page.waitForTimeout(500);
  }

  async saveAIConfig(endpoint: string, model: string, key?: string) {
    await this.switchTab("ai");
    // Wait for loadAIConfig() triggered by tab switch to settle
    await this.page.waitForTimeout(600);
    await this.aiEndpoint.fill(endpoint);
    await this.aiModel.fill(model);
    if (key) await this.aiKey.fill(key);
    await this.aiSaveBtn.click();
    await this.page.waitForResponse((r) =>
      r.url().includes("/ai-config") && r.request().method() === "POST"
    );
  }

  async getAIConfig(): Promise<{ endpoint: string; model: string }> {
    await this.switchTab("ai");
    await this.aiLoadBtn.click();
    await this.page.waitForTimeout(500);
    return {
      endpoint: (await this.aiEndpoint.inputValue()) ?? "",
      model: (await this.aiModel.inputValue()) ?? "",
    };
  }
}
