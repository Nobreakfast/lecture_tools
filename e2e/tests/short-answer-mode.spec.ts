// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { QuizPage } from "../pages/quiz.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";

test.describe("short_answer_mode on student quiz page", () => {
  test("student sees correct UI per short_answer_mode", async ({ browser }) => {
    const seed = getSeedResult();

    // Teacher: open entry
    const teacherCtx = await browser.newContext();
    const teacherPage = new TeacherPage(await teacherCtx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.uploadQuizYAML();
    await teacherPage.openEntry();

    // Student: join quiz
    const studentCtx = await browser.newContext();
    const studentPageObj = new StudentPage(await studentCtx.newPage());
    await studentPageObj.enterCode(seed.inviteCode);
    await studentPageObj.waitForQuizOpen();
    await studentPageObj.joinQuiz("模式学生", "2024099", "测试班级");

    const quizPage = new QuizPage(studentPageObj.page);
    await quizPage.waitForLoad();

    // q5: short_answer_mode=text → textarea only, no upload button
    const q5Card = quizPage.questions.locator(".card").filter({ hasText: "请描述你目前最困惑的知识点" });
    await expect(q5Card.locator("textarea")).toBeVisible();
    await expect(q5Card.locator(".upload-btn")).toHaveCount(0);

    // q6: short_answer_mode=image → upload button only, no textarea
    const q6Card = quizPage.questions.locator(".card").filter({ hasText: "请上传你的代码运行截图" });
    await expect(q6Card.locator(".upload-btn")).toBeVisible();
    await expect(q6Card.locator("textarea")).toHaveCount(0);

    // q7: short_answer_mode=code → textarea only, no upload button
    const q7Card = quizPage.questions.locator(".card").filter({ hasText: "请粘贴你的实现代码" });
    await expect(q7Card.locator("textarea")).toBeVisible();
    await expect(q7Card.locator(".upload-btn")).toHaveCount(0);
    const q7Placeholder = await q7Card.locator("textarea").getAttribute("placeholder");
    expect(q7Placeholder).toContain("代码");

    // q8: short_answer_mode=text_image → both textarea and upload
    const q8Card = quizPage.questions.locator(".card").filter({ hasText: "请描述你的解题思路并上传结果截图" });
    await expect(q8Card.locator("textarea")).toBeVisible();
    await expect(q8Card.locator(".upload-btn")).toBeVisible();

    // Cleanup
    await teacherPage.closeEntry();
    await teacherCtx.close();
    await studentCtx.close();
  });
});

test.describe("short_answer_mode in quiz editor preview", () => {
  test("editor preview shows correct UI per short_answer_mode", async ({ page }) => {
    const seed = getSeedResult();

    const teacherPage = new TeacherPage(page);
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);

    // Navigate to quiz editor
    await teacherPage.switchSubTab("sub-quiz");

    // Find the edit link for the current quiz and click it
    const editLink = page.locator('a[href*="quiz-editor"]').first();
    if (await editLink.isVisible({ timeout: 3000 }).catch(() => false)) {
      await editLink.click();
    } else {
      await page.goto(`/quiz-editor?course_id=${seed.courseId}&quiz_id=quiz.sample`);
    }
    await page.waitForLoadState("networkidle");

    // Switch to preview tab
    const previewTab = page.locator("text=预览").first();
    await previewTab.click();
    await page.waitForTimeout(500);

    // q5 (text mode): should have textarea, no upload button
    const q5Preview = page.locator(".preview-card").filter({ hasText: "请描述你目前最困惑的知识点" });
    await expect(q5Preview.locator("textarea")).toBeVisible();
    await expect(q5Preview.locator("button:has-text('上传图片')")).toHaveCount(0);

    // q6 (image mode): should have upload button, no textarea
    const q6Preview = page.locator(".preview-card").filter({ hasText: "请上传你的代码运行截图" });
    await expect(q6Preview.locator("button:has-text('上传图片')")).toBeVisible();
    await expect(q6Preview.locator("textarea")).toHaveCount(0);

    // q7 (code mode): should have textarea with code placeholder, no upload
    const q7Preview = page.locator(".preview-card").filter({ hasText: "请粘贴你的实现代码" });
    await expect(q7Preview.locator("textarea")).toBeVisible();
    await expect(q7Preview.locator("button:has-text('上传图片')")).toHaveCount(0);

    // q8 (text_image mode): both textarea and upload
    const q8Preview = page.locator(".preview-card").filter({ hasText: "请描述你的解题思路并上传结果截图" });
    await expect(q8Preview.locator("textarea")).toBeVisible();
    await expect(q8Preview.locator("button:has-text('上传图片')")).toBeVisible();
  });
});
