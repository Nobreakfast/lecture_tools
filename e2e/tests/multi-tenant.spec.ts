// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import {
  getSeedResult,
  TEACHER_ID, TEACHER_PASSWORD,
  TEACHER_B_ID, TEACHER_B_PASSWORD,
} from "../helpers/seed";
import { BASE_URL } from "../helpers/server";

test.describe("Multi-tenant isolation", () => {
  test("two teachers manage independent courses concurrently", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // ── Teacher A: login, select course A, upload quiz, open entry ──
    const ctxA = await browser.newContext();
    const teacherA = new TeacherPage(await ctxA.newPage());
    await teacherA.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherA.selectCourse(seed.courseId);
    await teacherA.uploadQuizYAML();
    // Clear previous attempts
    await teacherA.switchTab("tab-attempts");
    await teacherA.clearAttemptsBtn.click();
    const confirmA = teacherA.page.locator("#confirmModal");
    await confirmA.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    if (await confirmA.isVisible()) {
      await teacherA.confirmOkBtn.click();
      await teacherA.page.waitForTimeout(500);
    }
    await teacherA.openEntry();

    // ── Teacher B: login, select course B, upload quiz, open entry ──
    const ctxB = await browser.newContext();
    const teacherB = new TeacherPage(await ctxB.newPage());
    await teacherB.login(TEACHER_B_ID, TEACHER_B_PASSWORD);
    await teacherB.selectCourse(seed.courseIdB);
    await teacherB.uploadQuizYAML();
    await teacherB.switchTab("tab-attempts");
    await teacherB.clearAttemptsBtn.click();
    const confirmB = teacherB.page.locator("#confirmModal");
    await confirmB.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    if (await confirmB.isVisible()) {
      await teacherB.confirmOkBtn.click();
      await teacherB.page.waitForTimeout(500);
    }
    await teacherB.openEntry();

    // ── Student X: enters course A, answers, submits ──
    const ctxSX = await browser.newContext();
    const studentX = new StudentPage(await ctxSX.newPage());
    await studentX.enterCode(seed.inviteCode);
    await studentX.waitForQuizOpen();
    await studentX.joinQuiz("多租户学生X", "MT001", "租户班A");

    const quizX = new QuizPage(studentX.page);
    await quizX.waitForLoad();
    await quizX.answerAllViaAPI({
      q1: "B", q2: "Y", q3: "A,B,C", q4: "A", q5: "来自课程A",
    });
    await quizX.submit();
    const resultX = new ResultPage(studentX.page);
    await resultX.waitForLoad();

    // ── Student Y: enters course B, answers, submits ──
    const ctxSY = await browser.newContext();
    const studentY = new StudentPage(await ctxSY.newPage());
    await studentY.enterCode(seed.inviteCodeB);
    await studentY.waitForQuizOpen();
    await studentY.joinQuiz("多租户学生Y", "MT002", "租户班B");

    const quizY = new QuizPage(studentY.page);
    await quizY.waitForLoad();
    await quizY.answerAllViaAPI({
      q1: "B", q2: "Y", q3: "A,B,C", q4: "B", q5: "来自课程B",
    });
    await quizY.submit();
    const resultY = new ResultPage(studentY.page);
    await resultY.waitForLoad();

    // ── Teacher A: should see only Student X ──
    await teacherA.page.reload();
    await teacherA.viewMain.waitFor({ state: "visible" });
    await teacherA.selectCourse(seed.courseId);
    await teacherA.switchTab("tab-attempts");
    await teacherA.page.waitForTimeout(1500);

    await expect(
      teacherA.attemptsList.locator("text=多租户学生X")
    ).toBeVisible({ timeout: 5000 });
    await expect(
      teacherA.attemptsList.locator("text=多租户学生Y")
    ).not.toBeVisible();

    // ── Teacher B: should see only Student Y ──
    await teacherB.page.reload();
    await teacherB.viewMain.waitFor({ state: "visible" });
    await teacherB.selectCourse(seed.courseIdB);
    await teacherB.switchTab("tab-attempts");
    await teacherB.page.waitForTimeout(1500);

    await expect(
      teacherB.attemptsList.locator("text=多租户学生Y")
    ).toBeVisible({ timeout: 5000 });
    await expect(
      teacherB.attemptsList.locator("text=多租户学生X")
    ).not.toBeVisible();

    // ── Teacher A clears attempts → Student Y should still exist in B ──
    await teacherA.switchTab("tab-attempts");
    await teacherA.clearAttemptsBtn.click();
    const confirmA2 = teacherA.page.locator("#confirmModal");
    await confirmA2.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    if (await confirmA2.isVisible()) {
      await teacherA.confirmOkBtn.click();
      await teacherA.page.waitForTimeout(500);
    }
    await teacherA.page.waitForTimeout(1000);

    // Verify A is cleared
    await expect(
      teacherA.attemptsList.locator("text=多租户学生X")
    ).not.toBeVisible();

    // Verify B is untouched
    await teacherB.page.reload();
    await teacherB.viewMain.waitFor({ state: "visible" });
    await teacherB.selectCourse(seed.courseIdB);
    await teacherB.switchTab("tab-attempts");
    await teacherB.page.waitForTimeout(1500);

    await expect(
      teacherB.attemptsList.locator("text=多租户学生Y")
    ).toBeVisible({ timeout: 5000 });

    // ── Teacher B closes entry → course A entry still open ──
    await teacherB.closeEntry();
    const statusB = await teacherB.getEntryStatusText();
    expect(statusB).toContain("关闭");

    // Course A should still be open
    const statusA = await teacherA.getEntryStatusText();
    expect(statusA).toContain("开放");

    await ctxA.close();
    await ctxB.close();
    await ctxSX.close();
    await ctxSY.close();
  });

  test("teacher cannot access other teacher's course data", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // Teacher A logs in
    const ctxA = await browser.newContext();
    const pageA = await ctxA.newPage();
    const teacherA = new TeacherPage(pageA);
    await teacherA.login(TEACHER_ID, TEACHER_PASSWORD);

    // Teacher A tries to access Teacher B's course attempts via API → 403
    const attemptsResp = await pageA.evaluate(
      async ({ courseIdB }) => {
        const r = await fetch(
          `/api/teacher/courses/attempts?course_id=${courseIdB}`,
          { credentials: "include" }
        );
        return r.status;
      },
      { courseIdB: seed.courseIdB }
    );
    expect(attemptsResp).toBe(403);

    // Teacher A tries to access Teacher B's course state → 403
    const stateResp = await pageA.evaluate(
      async ({ courseIdB }) => {
        const r = await fetch(
          `/api/teacher/courses/state?course_id=${courseIdB}`,
          { credentials: "include" }
        );
        return r.status;
      },
      { courseIdB: seed.courseIdB }
    );
    expect(stateResp).toBe(403);

    // Teacher A tries to access Teacher B's attempt detail → 403
    // First, create an attempt in course B to get an ID
    const ctxB = await browser.newContext();
    const teacherBPage = new TeacherPage(await ctxB.newPage());
    await teacherBPage.login(TEACHER_B_ID, TEACHER_B_PASSWORD);
    await teacherBPage.selectCourse(seed.courseIdB);
    await teacherBPage.uploadQuizYAML();
    await teacherBPage.openEntry();

    const ctxS = await browser.newContext();
    const student = new StudentPage(await ctxS.newPage());
    await student.enterCode(seed.inviteCodeB);
    await student.waitForQuizOpen();
    await student.joinQuiz("跨域学生", "CROSS001", "跨域班");

    const quiz = new QuizPage(student.page);
    await quiz.waitForLoad();
    await quiz.answerAllViaAPI({
      q1: "B", q2: "Y", q3: "A,B,C", q4: "A", q5: "跨域测试",
    });
    await quiz.submit();

    // Get the attempt ID from Teacher B's view
    await teacherBPage.page.reload();
    await teacherBPage.viewMain.waitFor({ state: "visible" });
    await teacherBPage.selectCourse(seed.courseIdB);
    await teacherBPage.switchTab("tab-attempts");
    await teacherBPage.page.waitForTimeout(1500);

    const attemptId = await teacherBPage.page.evaluate(
      async ({ courseIdB }) => {
        const r = await fetch(
          `/api/teacher/courses/attempts?course_id=${courseIdB}`,
          { credentials: "include" }
        );
        const data = await r.json();
        return data.items?.[0]?.id ?? "";
      },
      { courseIdB: seed.courseIdB }
    );
    expect(attemptId).toBeTruthy();

    // Teacher A tries to access that attempt → 403
    const detailResp = await pageA.evaluate(
      async ({ attemptId, courseIdB }) => {
        const r = await fetch(
          `/api/teacher/courses/attempt-detail?id=${attemptId}&course_id=${courseIdB}`,
          { credentials: "include" }
        );
        return r.status;
      },
      { attemptId, courseIdB: seed.courseIdB }
    );
    expect(detailResp).toBe(403);

    // Also try with Teacher A's own course_id but B's attempt → 403
    const detailResp2 = await pageA.evaluate(
      async ({ attemptId, courseId }) => {
        const r = await fetch(
          `/api/teacher/courses/attempt-detail?id=${attemptId}&course_id=${courseId}`,
          { credentials: "include" }
        );
        return r.status;
      },
      { attemptId, courseId: seed.courseId }
    );
    expect(detailResp2).toBe(403);

    await ctxA.close();
    await ctxB.close();
    await ctxS.close();
  });

  test("same teacher manages two courses independently", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    // Teacher A logs in
    const ctxT = await browser.newContext();
    const teacher = new TeacherPage(await ctxT.newPage());
    await teacher.login(TEACHER_ID, TEACHER_PASSWORD);

    // Create a second course for Teacher A
    const slug = `e2e-multi-${Date.now()}`;
    await teacher.createCourse("多课程测试", slug);
    await teacher.page.waitForTimeout(500);

    // Get the new course ID from the pills
    const newCourseId = await teacher.page.evaluate(() => {
      const pills = document.querySelectorAll("#coursePills .course-pill");
      const last = pills[pills.length - 1] as HTMLElement;
      return parseInt(last.dataset.courseId || "0");
    });
    expect(newCourseId).toBeGreaterThan(0);

    // Upload quiz to original course A and open entry
    await teacher.selectCourse(seed.courseId);
    await teacher.uploadQuizYAML();
    await teacher.openEntry();

    // Upload quiz to new course C but do NOT open entry
    await teacher.selectCourse(newCourseId);
    await teacher.uploadQuizYAML();
    await teacher.closeEntry();

    // Student enters course A → can join
    const ctxS1 = await browser.newContext();
    const student1 = new StudentPage(await ctxS1.newPage());
    await student1.enterCode(seed.inviteCode);
    await student1.waitForQuizOpen();
    await expect(student1.quizForm).toBeVisible();

    // Student enters course C → should see waiting (entry closed)
    const ctxS2 = await browser.newContext();
    const student2page = await ctxS2.newPage();
    const student2 = new StudentPage(student2page);

    // Get invite code for course C via API
    const courseCInfo = await teacher.page.evaluate(
      async ({ cid }) => {
        const r = await fetch(
          `/api/teacher/courses?_=${Date.now()}`,
          { credentials: "include" }
        );
        const data = await r.json();
        const c = (data.items ?? []).find((c: any) => c.id === cid);
        return c?.invite_code ?? "";
      },
      { cid: newCourseId }
    );
    expect(courseCInfo).toBeTruthy();

    await student2.enterCode(courseCInfo);
    await student2.switchTab("tab-quiz");
    await expect(student2.quizWait).toBeVisible({ timeout: 10_000 });
    await expect(student2.quizForm).not.toBeVisible();

    // Student 1 joins and submits in course A
    await student1.joinQuiz("课程A学生", "MC001", "多课程班");
    const quiz1 = new QuizPage(student1.page);
    await quiz1.waitForLoad();
    await quiz1.answerAllViaAPI({
      q1: "B", q2: "Y", q3: "A,B,C", q4: "A", q5: "多课程测试A",
    });
    await quiz1.submit();

    // Verify Teacher sees attempt in course A
    await teacher.page.reload();
    await teacher.viewMain.waitFor({ state: "visible" });
    await teacher.selectCourse(seed.courseId);
    await teacher.switchTab("tab-attempts");
    await teacher.page.waitForTimeout(1500);
    await expect(
      teacher.attemptsList.locator("text=课程A学生")
    ).toBeVisible({ timeout: 5000 });

    // Course C should have no attempts
    await teacher.selectCourse(newCourseId);
    await teacher.switchTab("tab-attempts");
    await teacher.page.waitForTimeout(1000);
    await expect(
      teacher.attemptsList.locator("text=课程A学生")
    ).not.toBeVisible();

    // Clear attempts for course A → course C unaffected (both use same quiz_id)
    await teacher.selectCourse(seed.courseId);
    await teacher.switchTab("tab-attempts");
    await teacher.clearAttemptsBtn.click();
    const confirm = teacher.page.locator("#confirmModal");
    await confirm.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    if (await confirm.isVisible()) {
      await teacher.confirmOkBtn.click();
      await teacher.page.waitForTimeout(500);
    }

    await ctxT.close();
    await ctxS1.close();
    await ctxS2.close();
  });
});
