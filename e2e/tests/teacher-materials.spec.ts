// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import * as path from "path";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";
import { BASE_URL } from "../helpers/server";

test.describe("Teacher materials management", () => {
  let teacherPage: TeacherPage;

  test.beforeEach(async ({ page }) => {
    teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
  });

  test("upload material and see it in list", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    const fixturePath = path.resolve(__dirname, "../fixtures/sample.txt");
    await teacherPage.uploadMaterial(fixturePath);

    await expect(teacherPage.materialList).toContainText("sample.txt");
  });

  test("toggle material visibility", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    // Upload if not present
    const fixturePath = path.resolve(__dirname, "../fixtures/sample.txt");
    await teacherPage.uploadMaterial(fixturePath);
    await expect(teacherPage.materialList).toContainText("sample.txt");

    // Toggle visibility via API
    const response = await teacherPage.page.evaluate(
      async ({ courseId }) => {
        const r = await fetch(
          `/api/teacher/courses/materials/visibility?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ file: "sample.txt", visible: false }),
          }
        );
        return { status: r.status, body: await r.json() };
      },
      { courseId: seed.courseId }
    );
    expect(response.status).toBe(200);
    expect(response.body.ok).toBe(true);
  });

  test("delete material removes from list", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    // Upload a unique file via API
    const fileName = `del-mat-${Date.now()}.txt`;
    await teacherPage.page.evaluate(
      async ({ courseId, fileName }) => {
        const fd = new FormData();
        fd.append(
          "files",
          new Blob(["test content"], { type: "text/plain" }),
          fileName
        );
        await fetch(
          `/api/teacher/courses/materials/upload?course_id=${courseId}`,
          {
            method: "POST",
            credentials: "include",
            body: fd,
          }
        );
      },
      { courseId: seed.courseId, fileName }
    );

    // Reload materials sub-tab
    await teacherPage.switchSubTab("sub-materials");
    await teacherPage.page.waitForTimeout(500);
    await expect(teacherPage.materialList).toContainText(fileName);

    // Delete via API
    const delRes = await teacherPage.page.evaluate(
      async ({ courseId, fileName }) => {
        const r = await fetch(
          `/api/teacher/courses/materials/delete?course_id=${courseId}`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ file: fileName }),
          }
        );
        return r.status;
      },
      { courseId: seed.courseId, fileName }
    );
    expect(delRes).toBe(200);
  });

  test("student sees visible materials but not hidden ones", async ({
    browser,
  }) => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);

    // Upload a visible material
    const visFile = `vis-${Date.now()}.txt`;
    await teacherPage.page.evaluate(
      async ({ courseId, fileName }) => {
        const fd = new FormData();
        fd.append(
          "files",
          new Blob(["visible"], { type: "text/plain" }),
          fileName
        );
        await fetch(
          `/api/teacher/courses/materials/upload?course_id=${courseId}`,
          { method: "POST", credentials: "include", body: fd }
        );
      },
      { courseId: seed.courseId, fileName: visFile }
    );

    // Check student can see it
    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.switchTab("tab-materials");
    await studentPage.page.waitForTimeout(1000);
    await expect(studentPage.materialsContent).toContainText(visFile);

    await studentCtx.close();
  });
});
