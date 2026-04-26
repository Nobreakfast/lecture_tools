// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { ResultPage } from "../pages/result.page";
import { TEACHER_ID, TEACHER_PASSWORD, getSeedResult } from "../helpers/seed";

test.describe("Teacher panel", () => {
  let teacherPage: TeacherPage;

  test.beforeEach(async ({ page }) => {
    teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
  });

  test("login shows teacher name and main view", async () => {
    await expect(teacherPage.viewMain).toBeVisible();
    await expect(teacherPage.teacherName).not.toBeEmpty();
  });

  test("seeded course appears in course list", async () => {
    await teacherPage.switchTab("tab-courses");
    await expect(teacherPage.courseList).toContainText("E2E测试课程");
  });

  test("opens teacher docs in a new page", async () => {
    const popup = await teacherPage.openDocsPage();
    await expect(popup).toHaveURL(/\/teacher\/docs$/);
    await expect(popup.getByRole("heading", { name: "教师使用文档" })).toBeVisible();
    await expect(popup.locator("#docContent")).toContainText("课程助手教师使用说明");
    await popup.close();
  });

  test("create a new course", async () => {
    const slug = `e2e-new-${Date.now()}`;
    await teacherPage.createCourse("新建测试课程", slug);
    await expect(teacherPage.courseList).toContainText("新建测试课程");
  });

  test("create course keeps english display name with spaces but stores underscore internal name", async () => {
    const englishName = "Machine Learning Intro";
    await teacherPage.createCourse("英文名转换课程", englishName);
    await expect(teacherPage.courseList).toContainText("英文名转换课程");
    await expect(teacherPage.courseList).toContainText(englishName);

    const created = await teacherPage.page.evaluate(async () => {
      const r = await fetch(`/api/teacher/courses?_=${Date.now()}`, {
        credentials: "include",
      });
      const data = await r.json();
      return (data.items ?? []).find((item: any) => item.name === "英文名转换课程");
    });

    expect(created?.display_name).toBe("Machine Learning Intro");
    expect(created?.internal_name).toBe("Machine_Learning_Intro");
    expect(created?.slug).toBe("Machine_Learning_Intro");
  });

  test("create course with full-width characters normalizes to half-width", async () => {
    const fullWidthName = "\uFF2D\uFF2C\u3000\uFF11\uFF10\uFF11"; // ＭＬ　１０１
    await teacherPage.createCourse("全角测试课程", fullWidthName);
    await expect(teacherPage.courseList).toContainText("全角测试课程");

    const created = await teacherPage.page.evaluate(async () => {
      const r = await fetch(`/api/teacher/courses?_=${Date.now()}`, {
        credentials: "include",
      });
      const data = await r.json();
      return (data.items ?? []).find((item: any) => item.name === "全角测试课程");
    });

    expect(created?.display_name).toBe("ML 101");
    expect(created?.internal_name).toBe("ML_101");
  });

  test("remembers last selected course after reload", async () => {
    const firstCourseId = await teacherPage.page.evaluate(() => {
      const first = document.querySelector("#coursePills .course-pill") as HTMLElement | null;
      return first?.dataset.courseId || "";
    });
    expect(firstCourseId).toBeTruthy();

    const secondName = "记忆课程";
    await teacherPage.createCourse(secondName, `remember course ${Date.now()}`);

    const secondCourseId = await teacherPage.page.evaluate((courseName) => {
      const pills = Array.from(document.querySelectorAll("#coursePills .course-pill")) as HTMLElement[];
      const cards = Array.from(document.querySelectorAll("#courseList .course-card")) as HTMLElement[];
      const card = cards.find((item) => item.textContent?.includes(courseName));
      if (!card) return "";
      const id = card.id.replace("courseCard_", "");
      const pill = pills.find((item) => item.dataset.courseId === id);
      pill?.click();
      return id;
    }, secondName);
    expect(secondCourseId).toBeTruthy();
    expect(secondCourseId).not.toBe(firstCourseId);

    await teacherPage.page.reload();
    await teacherPage.viewMain.waitFor({ state: "visible" });

    const activeCourseId = await teacherPage.page.evaluate(() => {
      const active = document.querySelector("#coursePills .course-pill.active") as HTMLElement | null;
      return active?.dataset.courseId || "";
    });
    expect(activeCourseId).toBe(secondCourseId);
  });

  test("upload quiz YAML and see title", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.switchTab("tab-attempts");
    await expect(teacherPage.quizTitle).toContainText("第一周课堂反馈");
  });

  test("dedup by name keeps highest score for the same student", async ({
    browser,
  }) => {
    const seed = getSeedResult();
    const studentName = `去重学生${Date.now().toString().slice(-4)}`;
    const studentNo = `E2EDUP${Date.now().toString().slice(-6)}`;

    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    const submitAttempt = async (answers: Record<string, string>) => {
      const studentCtx = await browser.newContext();
      const studentPage = new StudentPage(await studentCtx.newPage());
      await studentPage.enterCode(seed.inviteCode);
      await studentPage.waitForQuizOpen();
      await studentPage.joinQuiz(studentName, studentNo, "测试班级");

      const quizPage = new QuizPage(studentPage.page);
      await quizPage.waitForLoad();
      await quizPage.answerAllViaAPI(answers);
      await quizPage.submit();

      const resultPage = new ResultPage(studentPage.page);
      await resultPage.waitForLoad();
      const score = await resultPage.getScore();
      await studentCtx.close();
      return score;
    };

    const lowScore = await submitAttempt({
      q1: "A",
      q2: "N",
      q3: "D",
      q4: "B",
      q5: "第一次较低分",
    });
    expect(lowScore.correct).toBe(0);

    const highScore = await submitAttempt({
      q1: "B",
      q2: "Y",
      q3: "A,B,C",
      q4: "A",
      q5: "第二次较高分",
    });
    expect(highScore.correct).toBe(3);
    expect(highScore.total).toBe(3);

    await teacherPage.switchTab("tab-attempts");
    await expect(teacherPage.attemptsList).toContainText(studentName);
    await expect(
      teacherPage.attemptsList.locator("tbody tr").filter({ hasText: studentName })
    ).toHaveCount(1);
  });

  test("toggle entry open and closed", async () => {
    const seed = getSeedResult();
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();

    await teacherPage.openEntry();
    let status = await teacherPage.getEntryStatusText();
    expect(status).toContain("开放");

    await teacherPage.closeEntry();
    status = await teacherPage.getEntryStatusText();
    expect(status).toContain("关闭");
  });

  test("attempts tab shows empty state initially", async () => {
    // Create a fresh course so there are no attempts
    const slug = `e2e-empty-${Date.now()}`;
    const courseName = "空课程";
    await teacherPage.createCourse(courseName, slug);
    const newCourseId = await teacherPage.page.evaluate((name) => {
      const cards = Array.from(document.querySelectorAll("#courseList .course-card")) as HTMLElement[];
      const card = cards.find((item) => item.textContent?.includes(name));
      return card ? card.id.replace("courseCard_", "") : "";
    }, courseName);
    expect(newCourseId).toBeTruthy();
    await teacherPage.selectCourse(Number(newCourseId));
    await teacherPage.switchTab("tab-attempts");
    // A new course without a loaded quiz should show the unloaded placeholder
    await expect(teacherPage.quizTitle).toHaveText("未加载");
  });

  test("change password", async () => {
    const newPassword = `newpwd-${Date.now()}`;
    await teacherPage.changePassword(TEACHER_PASSWORD, newPassword);

    // Verify: logout and login with new password
    await teacherPage.page.locator("text=退出登录").click();
    await teacherPage.page.waitForTimeout(500);

    const freshPage = new TeacherPage(teacherPage.page);
    await freshPage.login(TEACHER_ID, newPassword);
    await expect(freshPage.viewMain).toBeVisible();

    // Restore original password for other tests
    await freshPage.changePassword(newPassword, TEACHER_PASSWORD);
  });
});
