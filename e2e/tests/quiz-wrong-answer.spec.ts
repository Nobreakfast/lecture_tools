// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("Quiz wrong-answer scenarios", () => {
  test("student answers some questions wrong → partial score", async ({
    browser,
  }) => {
    const seed = getSeedResult();

    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.waitForQuizOpen();
    await studentPage.joinQuiz("错答学生", "2024W01", "测试班级");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();

    // q1: wrong (A instead of B), q2: wrong (N instead of Y), q3: correct
    await quiz.answerAllViaAPI({
      q1: "A",
      q2: "N",
      q3: "A,B,C",
      q4: "B",
      q5: "不太确定",
    });
    await quiz.submit();

    const result = new ResultPage(studentPage.page);
    await result.waitForLoad();

    const score = await result.getScore();
    expect(score.correct).toBe(1);
    expect(score.total).toBe(3);

    const results = await result.getQuestionResults();
    expect(results.ok).toBe(1);
    expect(results.bad).toBe(2);

    await teacherCtx.close();
    await studentCtx.close();
  });

  test("student answers all wrong → zero score", async ({ browser }) => {
    const seed = getSeedResult();

    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const studentCtx = await browser.newContext();
    const studentPage = new StudentPage(await studentCtx.newPage());
    await studentPage.enterCode(seed.inviteCode);
    await studentPage.waitForQuizOpen();
    await studentPage.joinQuiz("全错学生", "2024W02", "测试班级");

    const quiz = new QuizPage(studentPage.page);
    await quiz.waitForLoad();

    // All scored questions wrong
    await quiz.answerAllViaAPI({
      q1: "C",
      q2: "N",
      q3: "D",
      q4: "C",
      q5: "什么都不懂",
    });
    await quiz.submit();

    const result = new ResultPage(studentPage.page);
    await result.waitForLoad();

    const score = await result.getScore();
    expect(score.correct).toBe(0);
    expect(score.total).toBe(3);

    await teacherCtx.close();
    await studentCtx.close();
  });
});
