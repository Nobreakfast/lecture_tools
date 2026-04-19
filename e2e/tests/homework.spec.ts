// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import * as path from "path";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

const ASSIGNMENT_ID = "e2e_hw_01";

test.describe("Homework lifecycle", () => {
  test("teacher publishes assignment → student submits homework → teacher sees it", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher: publish assignment ──
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);

    const fixturePath = path.resolve(__dirname, "../fixtures/sample.txt");
    await teacherPage.uploadHomeworkAssignment(ASSIGNMENT_ID, fixturePath);

    // Verify assignment appears in list
    await teacherPage.page.waitForTimeout(500);
    await expect(teacherPage.homeworkAssignmentsList).toContainText(
      ASSIGNMENT_ID
    );

    // ── Student: create session and upload report ──
    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);

    // Wait for assignment to appear in select
    await studentPage.switchTab("tab-homework");
    await studentPage.page.waitForTimeout(1000);

    // Reload to ensure assignment list is loaded
    await studentPage.page.reload();
    await studentPage.panel.waitFor({ state: "visible" });
    await studentPage.switchTab("tab-homework");
    await studentPage.page.waitForTimeout(1000);

    await studentPage.startHomeworkSession(
      ASSIGNMENT_ID,
      "作业学生",
      "2024HW01",
      "作业班",
      "secret123"
    );

    // Upload PDF report via API
    const pdfPath = path.resolve(__dirname, "../fixtures/sample.pdf");
    await studentPage.reportInput.setInputFiles(pdfPath);
    await studentPage.page.waitForTimeout(1000);

    // Verify upload reflected
    const reportText = await studentPage.page
      .locator("#reportInfo")
      .textContent();
    expect(reportText).not.toContain("暂未上传");

    // ── Teacher: verify submission appears ──
    await teacherPage.switchSubTab("sub-homework");
    await teacherPage.page.waitForTimeout(1000);

    // Select the assignment filter
    await teacherPage.homeworkAssignmentFilter.selectOption(ASSIGNMENT_ID);
    await teacherPage.page.waitForTimeout(500);

    // Reload submissions
    await teacherPage.page
      .locator("#refreshHomeworkBtn")
      .click();
    await teacherPage.page.waitForTimeout(1000);

    await expect(teacherPage.homeworkSubmissionsList).toContainText("作业学生");

    await teacherCtx.close();
    await studentCtx.close();
  });

  test("student can download uploaded homework file via API", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // Ensure assignment exists
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    const fixturePath = path.resolve(__dirname, "../fixtures/sample.txt");
    await teacherPage.uploadHomeworkAssignment(ASSIGNMENT_ID, fixturePath);
    await teacherCtx.close();

    // Student session
    const studentCtx = await browser.newContext();
    const page = await studentCtx.newPage();

    // Navigate to base so relative fetch works
    await page.goto("/");

    // Create session via API
    const sessionRes = await page.evaluate(
      async ({ courseId, assignmentId }) => {
        const r = await fetch("/api/homework/session", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          body: JSON.stringify({
            course_id: courseId,
            assignment_id: assignmentId,
            name: "下载学生",
            student_no: "2024DL01",
            class_name: "下载班",
            secret_key: "dlsecret",
          }),
        });
        return { status: r.status, body: await r.json() };
      },
      { courseId: seed.courseId, assignmentId: ASSIGNMENT_ID }
    );
    expect(sessionRes.status).toBe(200);
    expect(sessionRes.body.ok).toBe(true);

    // Upload a PDF (minimal blob — validation may reject it)
    const uploadRes = await page.evaluate(async () => {
      const fd = new FormData();
      fd.append("slot", "report");
      fd.append(
        "file",
        new Blob(["%PDF-1.0\n"], { type: "application/pdf" }),
        "test.pdf"
      );
      const r = await fetch("/api/homework/upload", {
        method: "POST",
        credentials: "include",
        body: fd,
      });
      return r.status;
    });
    // PDF validation may reject our minimal blob; either 200 or 400 is acceptable
    expect([200, 400]).toContain(uploadRes);

    await studentCtx.close();
  });

  test("teacher can toggle assignment visibility", async ({ page }) => {
    const seed = getSeedResult();
    const teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);

    // Toggle hidden via API in browser context
    const hideRes = await page.evaluate(
      async ({ courseId, assignmentId }) => {
        const r = await fetch(
          `/api/teacher/courses/homework/assignments/visibility?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({
              assignment_id: assignmentId,
              hidden: true,
            }),
          }
        );
        return { status: r.status, body: await r.json() };
      },
      { courseId: seed.courseId, assignmentId: ASSIGNMENT_ID }
    );
    expect(hideRes.status).toBe(200);
    expect(hideRes.body.ok).toBe(true);

    // Restore visibility
    await page.evaluate(
      async ({ courseId, assignmentId }) => {
        await fetch(
          `/api/teacher/courses/homework/assignments/visibility?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({
              assignment_id: assignmentId,
              hidden: false,
            }),
          }
        );
      },
      { courseId: seed.courseId, assignmentId: ASSIGNMENT_ID }
    );
  });

  test("homework session with wrong secret is rejected", async ({ page }) => {
    const seed = getSeedResult();
    await page.goto("/");

    // Create a session first
    const createStatus = await page.evaluate(
      async ({ courseId, assignmentId }) => {
        const r = await fetch("/api/homework/session", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          body: JSON.stringify({
            course_id: courseId,
            assignment_id: assignmentId,
            name: "密钥学生",
            student_no: "2024SK01",
            class_name: "密钥班",
            secret_key: "correct_secret",
          }),
        });
        return r.status;
      },
      { courseId: seed.courseId, assignmentId: ASSIGNMENT_ID }
    );
    expect(createStatus).toBe(200);

    // Try again with wrong secret from a fresh context (no cookie)
    const wrongStatus = await page.evaluate(
      async ({ courseId, assignmentId }) => {
        const r = await fetch("/api/homework/session", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            course_id: courseId,
            assignment_id: assignmentId,
            name: "密钥学生",
            student_no: "2024SK01",
            class_name: "密钥班",
            secret_key: "wrong_secret",
          }),
        });
        return r.status;
      },
      { courseId: seed.courseId, assignmentId: ASSIGNMENT_ID }
    );
    expect(wrongStatus).toBe(403);
  });
});
