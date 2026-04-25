// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page, type Locator } from "@playwright/test";

export class StudentPage {
  readonly page: Page;

  // Landing
  readonly landing: Locator;
  readonly codeInput: Locator;
  readonly codeBtn: Locator;
  readonly codeError: Locator;

  // Panel
  readonly panel: Locator;
  readonly hdrCourseName: Locator;
  readonly hdrTeacher: Locator;

  // Quiz tab
  readonly quizLoading: Locator;
  readonly quizResume: Locator;
  readonly quizResumeBtn: Locator;
  readonly quizSignoutBtn: Locator;
  readonly quizWait: Locator;
  readonly quizForm: Locator;
  readonly quizTitle: Locator;
  readonly qzName: Locator;
  readonly qzStudentNo: Locator;
  readonly qzClassName: Locator;
  readonly quizJoinBtn: Locator;

  // Materials tab
  readonly materialsContent: Locator;

  // Homework tab
  readonly assignmentSelect: Locator;
  readonly hwName: Locator;
  readonly hwStudentNo: Locator;
  readonly hwClassName: Locator;
  readonly hwSecretKey: Locator;
  readonly enterBtn: Locator;
  readonly submissionCard: Locator;
  readonly reportUploadBtn: Locator;
  readonly reportInput: Locator;
  readonly reportInfo: Locator;
  readonly othersUploadBtn: Locator;
  readonly othersInput: Locator;
  readonly othersFileList: Locator;
  readonly sessionMessage: Locator;
  readonly submissionMessage: Locator;

  constructor(page: Page) {
    this.page = page;
    this.landing = page.locator("#landing");
    this.codeInput = page.locator("#landing #codeInput");
    this.codeBtn = page.locator("#codeBtn");
    this.codeError = page.locator("#codeError");

    this.panel = page.locator("#panel");
    this.hdrCourseName = page.locator("#hdrCourseName");
    this.hdrTeacher = page.locator("#hdrTeacher");

    this.quizLoading = page.locator("#quizLoading");
    this.quizResume = page.locator("#quizResume");
    this.quizResumeBtn = page.locator("#quizResumeBtn");
    this.quizSignoutBtn = page.locator("#quizSignoutBtn");
    this.quizWait = page.locator("#quizWait");
    this.quizForm = page.locator("#quizForm");
    this.quizTitle = page.locator("#quizTitle");
    this.qzName = page.locator("#qz_name");
    this.qzStudentNo = page.locator("#qz_student_no");
    this.qzClassName = page.locator("#qz_class_name");
    this.quizJoinBtn = page.locator("#quizJoinBtn");

    this.materialsContent = page.locator("#materialsContent");

    this.assignmentSelect = page.locator("#assignment_id");
    this.hwName = page.locator("#hw_name");
    this.hwStudentNo = page.locator("#hw_student_no");
    this.hwClassName = page.locator("#hw_class_name");
    this.hwSecretKey = page.locator("#hw_secret_key");
    this.enterBtn = page.locator("#enterBtn");
    this.submissionCard = page.locator("#submissionCard");
    this.reportUploadBtn = page.locator("#reportUploadBtn");
    this.reportInput = page.locator("#reportInput");
    this.reportInfo = page.locator("#reportInfo");
    this.othersUploadBtn = page.locator("#othersUploadBtn");
    this.othersInput = page.locator("#othersInput");
    this.othersFileList = page.locator("#othersFileList");
    this.sessionMessage = page.locator("#sessionMessage");
    this.submissionMessage = page.locator("#submissionMessage");
  }

  async goto() {
    await this.page.goto("/");
  }

  async enterCode(code: string) {
    await this.goto();
    await this.codeInput.fill(code);
    await this.codeBtn.click();
    await this.panel.waitFor({ state: "visible", timeout: 5000 });
  }

  async switchTab(tab: "tab-quiz" | "tab-materials" | "tab-homework") {
    await this.page.locator(`.tab-btn[data-tab="${tab}"]`).click();
    await this.page.locator(`#${tab}`).waitFor({ state: "visible" });
  }

  async waitForQuizOpen() {
    await this.switchTab("tab-quiz");
    await this.quizForm.waitFor({ state: "visible", timeout: 15_000 });
  }

  async waitForQuizResume() {
    await this.switchTab("tab-quiz");
    await this.quizResume.waitFor({ state: "visible", timeout: 15_000 });
  }

  async joinQuiz(name: string, studentNo: string, className: string) {
    await this.qzName.fill(name);
    await this.qzStudentNo.fill(studentNo);
    await this.qzClassName.fill(className);
    await this.quizJoinBtn.click();
    await this.page.waitForURL("**/quiz", { waitUntil: "domcontentloaded", timeout: 10_000 });
  }

  async startHomeworkSession(
    assignmentId: string,
    name: string,
    studentNo: string,
    className: string,
    secret: string
  ) {
    await this.switchTab("tab-homework");
    await this.assignmentSelect.selectOption(assignmentId);
    await this.hwName.fill(name);
    await this.hwStudentNo.fill(studentNo);
    await this.hwClassName.fill(className);
    await this.hwSecretKey.fill(secret);
    await this.enterBtn.click();
    await this.submissionCard.waitFor({ state: "visible", timeout: 5000 });
  }
}
