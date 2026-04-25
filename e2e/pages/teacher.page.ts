// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page, type Locator } from "@playwright/test";
import * as path from "path";

export class TeacherPage {
  readonly page: Page;
  readonly loginId: Locator;
  readonly loginPwd: Locator;
  readonly loginBtn: Locator;
  readonly viewMain: Locator;
  readonly teacherName: Locator;
  readonly coursePills: Locator;
  readonly docsButton: Locator;

  // Course tab
  readonly newCourseName: Locator;
  readonly newCourseSlug: Locator;
  readonly createCourseBtn: Locator;
  readonly courseList: Locator;

  // Attempts tab
  readonly quizTitle: Locator;
  readonly entryStatus: Locator;
  readonly liveStarted: Locator;
  readonly liveSubmitted: Locator;
  readonly openEntryBtn: Locator;
  readonly closeEntryBtn: Locator;
  readonly exportCsvBtn: Locator;
  readonly clearAttemptsBtn: Locator;
  readonly attemptsList: Locator;
  readonly attemptsQuizFilter: Locator;

  // Upload tab — quiz
  readonly yamlFileInput: Locator;
  readonly uploadYamlBtn: Locator;

  // Upload tab — quiz bank
  readonly quizBankIdInput: Locator;
  readonly quizBankFiles: Locator;
  readonly uploadQuizBankBtn: Locator;
  readonly quizBankList: Locator;
  readonly quizBankUploadResults: Locator;

  // Upload tab — materials
  readonly materialFile: Locator;
  readonly uploadMaterialBtn: Locator;
  readonly materialList: Locator;
  readonly materialUploadResults: Locator;

  // Upload tab — homework
  readonly homeworkAssignmentIdInput: Locator;
  readonly homeworkAssignmentFiles: Locator;
  readonly uploadHomeworkBtn: Locator;
  readonly homeworkAssignmentsList: Locator;
  readonly homeworkSubmissionsList: Locator;
  readonly homeworkAssignmentFilter: Locator;
  readonly refreshHomeworkBtn: Locator;

  // Summary tab
  readonly genSummaryBtn: Locator;
  readonly summaryContent: Locator;
  readonly summaryRawStats: Locator;
  readonly genHistoryBtn: Locator;
  readonly historyContent: Locator;
  readonly historyRawStats: Locator;

  // Modals
  readonly changePwdModal: Locator;
  readonly confirmModal: Locator;
  readonly confirmOkBtn: Locator;
  readonly confirmCancelBtn: Locator;
  readonly detailModal: Locator;
  readonly detailTitle: Locator;
  readonly detailContent: Locator;

  constructor(page: Page) {
    this.page = page;
    this.loginId = page.locator("#loginId");
    this.loginPwd = page.locator("#loginPwd");
    this.loginBtn = page.locator("#loginBtn");
    this.viewMain = page.locator("#view-main");
    this.teacherName = page.locator("#teacherName");
    this.coursePills = page.locator("#coursePills");
    this.docsButton = page.getByRole("button", { name: "使用文档" });

    this.newCourseName = page.locator("#newCourseName");
    this.newCourseSlug = page.locator("#newCourseSlug");
    this.createCourseBtn = page.locator("#createCourseBtn");
    this.courseList = page.locator("#courseList");

    this.quizTitle = page.locator("#quizTitle");
    this.entryStatus = page.locator("#entryStatus");
    this.liveStarted = page.locator("#liveStarted");
    this.liveSubmitted = page.locator("#liveSubmitted");
    this.openEntryBtn = page.locator("#openEntryBtn");
    this.closeEntryBtn = page.locator("#closeEntryBtn");
    this.exportCsvBtn = page.locator("#exportCsvBtn");
    this.clearAttemptsBtn = page.locator("#clearAttemptsBtn");
    this.attemptsList = page.locator("#attemptsList");
    this.attemptsQuizFilter = page.locator("#attemptsQuizFilter");

    this.yamlFileInput = page.locator("#yamlFileInput");
    this.uploadYamlBtn = page.locator("#uploadYamlBtn");

    this.quizBankIdInput = page.locator("#quizBankIdInput");
    this.quizBankFiles = page.locator("#quizBankFiles");
    this.uploadQuizBankBtn = page.locator("#uploadQuizBankBtn");
    this.quizBankList = page.locator("#quizBankList");
    this.quizBankUploadResults = page.locator("#quizBankUploadResults");

    this.materialFile = page.locator("#materialFile");
    this.uploadMaterialBtn = page.locator("#uploadMaterialBtn");
    this.materialList = page.locator("#materialList");
    this.materialUploadResults = page.locator("#materialUploadResults");

    this.homeworkAssignmentIdInput = page.locator("#homeworkAssignmentIdInput");
    this.homeworkAssignmentFiles = page.locator("#homeworkAssignmentFiles");
    this.uploadHomeworkBtn = page.locator("#uploadHomeworkBtn");
    this.homeworkAssignmentsList = page.locator("#homeworkAssignmentsList");
    this.homeworkSubmissionsList = page.locator("#homeworkSubmissionsList");
    this.homeworkAssignmentFilter = page.locator("#homeworkAssignmentFilter");
    this.refreshHomeworkBtn = page.locator("#refreshHomeworkBtn");

    this.genSummaryBtn = page.locator("#genSummaryBtn");
    this.summaryContent = page.locator("#summaryContent");
    this.summaryRawStats = page.locator("#summaryRawStats");
    this.genHistoryBtn = page.locator("#genHistoryBtn");
    this.historyContent = page.locator("#historyContent");
    this.historyRawStats = page.locator("#historyRawStats");

    this.changePwdModal = page.locator("#changePwdModal");
    this.confirmModal = page.locator("#confirmModal");
    this.confirmOkBtn = page.locator("#confirmOkBtn");
    this.confirmCancelBtn = page.locator("#confirmCancelBtn");
    this.detailModal = page.locator("#detailModal");
    this.detailTitle = page.locator("#detailTitle");
    this.detailContent = page.locator("#detailContent");
  }

  async goto() {
    await this.page.goto("/teacher");
  }

  async login(id: string, password: string) {
    await this.goto();
    await this.loginId.fill(id);
    await this.loginPwd.fill(password);
    await this.loginBtn.click();
    await this.viewMain.waitFor({ state: "visible" });
  }

  async switchTab(tab: "tab-courses" | "tab-attempts" | "tab-upload" | "tab-summary") {
    await this.page.locator(`.tab-btn[data-tab="${tab}"]`).click();
    await this.page.locator(`#${tab}`).waitFor({ state: "visible" });
  }

  async selectCourse(courseId: number) {
    await this.coursePills
      .locator(`.course-pill[data-course-id="${courseId}"]`)
      .click();
    await this.page.waitForTimeout(500);
  }

  async createCourse(name: string, slug: string) {
    await this.switchTab("tab-courses");
    await this.newCourseName.fill(name);
    await this.newCourseSlug.fill(slug);
    await this.createCourseBtn.click();
    await this.page.waitForTimeout(500);
  }

  async uploadQuizYAML() {
    await this.switchTab("tab-upload");
    const fixturePath = path.resolve(__dirname, "../fixtures/quiz.sample.yaml");
    await this.yamlFileInput.setInputFiles(fixturePath);
    await this.uploadYamlBtn.click();
    await this.page.waitForTimeout(500);
  }

  async openEntry() {
    await this.switchTab("tab-attempts");
    await this.openEntryBtn.click();
    await this.page.waitForTimeout(300);
  }

  async closeEntry() {
    await this.switchTab("tab-attempts");
    await this.closeEntryBtn.click();
    await this.page.waitForTimeout(300);
  }

  async getEntryStatusText(): Promise<string> {
    return (await this.entryStatus.textContent()) ?? "";
  }

  async getLiveStats() {
    return {
      started: (await this.liveStarted.textContent()) ?? "0",
      submitted: (await this.liveSubmitted.textContent()) ?? "0",
    };
  }

  async getAttemptCount(): Promise<number> {
    await this.switchTab("tab-attempts");
    await this.page.waitForTimeout(300);
    const rows = this.attemptsList.locator("tr");
    const count = await rows.count();
    // Subtract header row if present
    const headerCount = await this.attemptsList.locator("thead tr").count();
    return Math.max(0, count - headerCount);
  }

  async changePassword(oldPwd: string, newPwd: string) {
    await this.page.getByRole("button", { name: "修改密码" }).click();
    await this.changePwdModal.waitFor({ state: "visible" });
    await this.page.locator("#oldPassword").fill(oldPwd);
    await this.page.locator("#newPassword").fill(newPwd);
    await this.page.locator("#confirmPassword").fill(newPwd);
    await this.page.locator("#changePwdBtn").click();
    await this.page.waitForTimeout(500);
  }

  async openDocsPage() {
    const popupPromise = this.page.waitForEvent("popup");
    await this.docsButton.click();
    const popup = await popupPromise;
    await popup.waitForLoadState("domcontentloaded");
    return popup;
  }

  async switchSubTab(subtab: "sub-quiz" | "sub-materials" | "sub-homework") {
    await this.switchTab("tab-upload");
    await this.page
      .locator(`.sub-tab-btn[data-subtab="${subtab}"]`)
      .click();
    await this.page.locator(`#${subtab}`).waitFor({ state: "visible" });
  }

  async uploadMaterial(filePath: string) {
    await this.switchSubTab("sub-materials");
    await this.materialFile.setInputFiles(filePath);
    await this.uploadMaterialBtn.click();
    await this.page.waitForTimeout(500);
  }

  async uploadHomeworkAssignment(assignmentId: string, filePath: string) {
    await this.switchSubTab("sub-homework");
    await this.homeworkAssignmentIdInput.fill(assignmentId);
    await this.homeworkAssignmentFiles.setInputFiles(filePath);
    await this.uploadHomeworkBtn.click();
    await this.page.waitForTimeout(500);
  }

  async viewAttemptDetail(studentName: string) {
    await this.switchTab("tab-attempts");
    const row = this.attemptsList.locator("tr", {
      has: this.page.locator("td", { hasText: studentName }),
    });
    await row.locator("button", { hasText: "查看" }).click();
    await this.detailModal.waitFor({ state: "visible" });
  }

  async closeDetailModal() {
    await this.detailModal.locator("button", { hasText: "关闭" }).click();
    await this.page.waitForTimeout(300);
  }

  async deleteCourse(courseId: number, _courseName: string) {
    await this.switchTab("tab-courses");
    const card = this.page.locator(`#courseCard_${courseId}`);
    await card.locator("button", { hasText: "删除" }).click();
    await this.confirmOkBtn.waitFor({ state: "visible" });
    await this.confirmOkBtn.click();
    await this.page.waitForTimeout(500);
  }

  async downloadInviteQR(courseId: number): Promise<string> {
    await this.switchTab("tab-courses");
    const downloadPromise = this.page.waitForEvent("download");
    await this.page
      .locator(`#courseCard_${courseId}`)
      .locator("button", { hasText: "二维码" })
      .click();
    const download = await downloadPromise;
    return download.suggestedFilename();
  }
}
