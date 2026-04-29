// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { TeacherPage } from "../pages/teacher.page";
import { StudentPage } from "../pages/student.page";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";
import * as path from "path";

const ASSIGNMENT_ID = "e2e_qa_01";

test.describe("Q&A Issue lifecycle", () => {
    test("student creates issue → adds messages → teacher sees and replies", async ({
        browser,
    }) => {
        const seed = getSeedResult();

        // ── Teacher: publish an assignment first ──
        const teacherCtx = await browser.newContext();
        const teacherPage = new TeacherPage(await teacherCtx.newPage());
        await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
        await teacherPage.selectCourse(seed.courseId);

        const fixturePath = path.resolve(__dirname, "../fixtures/sample.txt");
        await teacherPage.uploadHomeworkAssignment(ASSIGNMENT_ID, fixturePath);
        await teacherPage.page.waitForTimeout(500);

        // ── Student: create a homework session so we have auth cookie ──
        const studentCtx = await browser.newContext();
        const studentPage = new StudentPage(await studentCtx.newPage());
        await studentPage.enterCode(seed.inviteCode);
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);
        await studentPage.page.reload();
        await studentPage.panel.waitFor({ state: "visible" });
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);

        await studentPage.startHomeworkSession(
            ASSIGNMENT_ID,
            "QA学生",
            "2024QA01",
            "QA班",
            "qasecret"
        );

        // ── Student: navigate to Q&A page (no invite param = authenticated mode) ──
        const qaUrl =
            `/student/qa?course_id=${seed.courseId}&assignment_id=${ASSIGNMENT_ID}`;
        await studentPage.page.goto(qaUrl);
        await studentPage.page.waitForTimeout(1000);

        // Verify Q&A page loaded
        await expect(studentPage.page.locator("h1")).toContainText("Q&A");

        // ── Student: create a new issue ──
        await studentPage.page.locator("#newIssueBtn").click();
        await studentPage.page.waitForTimeout(300);
        await studentPage.page
            .locator("#newIssueMsg")
            .fill("这道题我不太理解，什么是递归？");
        await studentPage.page.locator("button:text('提交')").last().click();
        await studentPage.page.waitForTimeout(1500);

        // Verify we're now in issue detail view
        await expect(
            studentPage.page.locator("#detailHeader")
        ).toContainText("什么是递归");
        await expect(
            studentPage.page.locator("#detailHeader")
        ).toContainText("进行中");

        // Verify first message appears in thread
        const messages = studentPage.page.locator("#detailMessages .msg-item");
        await expect(messages).toHaveCount(1);
        await expect(messages.first()).toContainText("什么是递归");
        await expect(
            messages.first().locator(".msg-sender")
        ).toContainText("学生");

        // ── Student: add a follow-up message ──
        await studentPage.page.locator("#replyMsg").fill("能举个例子吗？");
        await studentPage.page
            .locator("#replyForm button:text('回复')")
            .click();
        await studentPage.page.waitForTimeout(1500);

        // Verify two messages now
        await expect(
            studentPage.page.locator("#detailMessages .msg-item")
        ).toHaveCount(2);

        // ── Student: go back to issue list ──
        await studentPage.page.locator("button:text('返回列表')").click();
        await studentPage.page.waitForTimeout(500);

        // Verify issue appears in list with 2 messages
        const issueItems = studentPage.page.locator(".issue-list-item");
        await expect(issueItems).toHaveCount(1);
        await expect(issueItems.first()).toContainText("什么是递归");
        await expect(issueItems.first()).toContainText("💬 2");

        // ── Teacher: check Q&A issues via the new teacher QA page ──
        await teacherPage.page.goto(
            `/teacher/qa?course_id=${seed.courseId}&assignment_id=${ASSIGNMENT_ID}`
        );
        await teacherPage.page.waitForTimeout(2000);

        // Verify teacher QA page loaded
        await expect(teacherPage.page.locator("#pageTitle")).toContainText("Q&A 管理");

        // Verify the issue appears in teacher's list
        const teacherIssues = teacherPage.page.locator(".issue-list-item");
        await expect(teacherIssues).toHaveCount(1);
        await expect(teacherIssues.first()).toContainText("什么是递归");
        await expect(teacherIssues.first()).toContainText("💬 2");

        const issueIdText = await teacherIssues.first().locator(".issue-id").textContent();
        const issueId = parseInt(issueIdText!.replace("#", ""), 10);

        // ── Teacher: open the issue and reply ──
        await teacherIssues.first().click();
        await teacherPage.page.waitForTimeout(1000);

        // Verify detail view
        await expect(teacherPage.page.locator("#detailHeader")).toContainText("什么是递归");
        await expect(teacherPage.page.locator("#teacherActions")).toBeVisible();

        // Verify teacher actions are visible
        await expect(teacherPage.page.locator("#teacherActions button:text('置顶')")).toBeVisible();
        await expect(teacherPage.page.locator("#teacherActions button:text('隐藏')")).toBeVisible();
        await expect(teacherPage.page.locator("#teacherActions button:text('标记已解决')")).toBeVisible();

        // Teacher replies
        await teacherPage.page.locator("#replyMsg").fill("递归就是函数调用自身。比如计算阶乘：f(n)=n*f(n-1)");
        await teacherPage.page.locator("#replyForm button:text('回复')").click();
        await teacherPage.page.waitForTimeout(1500);

        // Verify teacher reply appears
        const teacherMsgs = teacherPage.page.locator("#detailMessages .msg-item");
        await expect(teacherMsgs).toHaveCount(3);
        await expect(teacherMsgs.nth(2)).toContainText("递归就是函数调用自身");
        await expect(teacherMsgs.nth(2).locator(".msg-sender")).toContainText("教师");

        // ── Teacher: pin the issue ──
        await teacherPage.page.locator("#teacherActions button:text('置顶')").click();
        await teacherPage.page.waitForTimeout(1000);

        // Verify pin action is now "取消置顶"
        await expect(teacherPage.page.locator("#teacherActions button:text('取消置顶')")).toBeVisible();

        // ── Teacher: resolve the issue ──
        await teacherPage.page.locator("#teacherActions button:text('标记已解决')").click();
        await teacherPage.page.waitForTimeout(1000);

        // Verify resolved status
        await expect(teacherPage.page.locator("#detailHeader")).toContainText("已解决");
        await expect(teacherPage.page.locator("#teacherActions button:text('重新打开')")).toBeVisible();

        // ── Teacher: go back to list and verify pin badge ──
        await teacherPage.page.locator("button:text('返回列表')").click();
        await teacherPage.page.waitForTimeout(500);

        const pinnedItem = teacherPage.page.locator(".issue-list-item.pinned");
        await expect(pinnedItem).toHaveCount(1);
        await expect(pinnedItem.first()).toContainText("什么是递归");
        await expect(pinnedItem.first()).toContainText("已解决");

        // ── Student: verify teacher reply via focus param ──
        await studentPage.page.goto(`${qaUrl}&focus=${issueId}`);
        await studentPage.page.waitForTimeout(2000);

        // Should now have 3 messages (2 student + 1 teacher)
        await expect(
            studentPage.page.locator("#detailMessages .msg-item")
        ).toHaveCount(3);

        // Verify teacher message content
        const teacherMsg = studentPage.page
            .locator("#detailMessages .msg-item")
            .nth(2);
        await expect(teacherMsg).toContainText("递归就是函数调用自身");
        await expect(teacherMsg.locator(".msg-sender")).toContainText("教师");

        // ── Student: verify resolved status ──
        await expect(studentPage.page.locator("#detailHeader")).toContainText(
            "已解决"
        );

        // ── Teacher: verify "返回教师面板" button ──
        await teacherPage.page.goto(
            `/teacher/qa?course_id=${seed.courseId}&assignment_id=${ASSIGNMENT_ID}`
        );
        await teacherPage.page.waitForTimeout(1000);
        const backBtn = teacherPage.page.locator("#backBtn");
        await expect(backBtn).toContainText("返回教师面板");

        // Cleanup
        await teacherCtx.close();
        await studentCtx.close();
    });

    test("share link allows read-only viewing of an issue", async ({
        browser,
    }) => {
        const seed = getSeedResult();

        // Create an issue via API first
        const studentCtx = await browser.newContext();
        const studentPage = new StudentPage(await studentCtx.newPage());
        await studentPage.enterCode(seed.inviteCode);
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);
        await studentPage.page.reload();
        await studentPage.panel.waitFor({ state: "visible" });
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);

        await studentPage.startHomeworkSession(
            ASSIGNMENT_ID,
            "分享学生",
            "2024QA02",
            "分享班",
            "share123"
        );

        // Create issue via API
        const createRes = await studentPage.page.evaluate(
            async (courseId: number) => {
                const fd = new FormData();
                fd.append("course_id", String(courseId));
                fd.append("assignment_id", "e2e_qa_01");
                fd.append("message", "这是一个用于测试分享链接的问题");
                const r = await fetch("/api/qa/issue/create", {
                    method: "POST",
                    credentials: "include",
                    body: fd,
                });
                return r.json();
            },
            seed.courseId
        );
        expect(createRes.ok).toBe(true);
        const issueId = createRes.issue_id;

        // Access via share link (new browser context, no auth)
        const shareCtx = await browser.newContext();
        const sharePage = await shareCtx.newPage();
        const shareUrl = `/student/qa?invite=${seed.inviteCode}&issue_id=${issueId}`;
        await sharePage.goto(shareUrl);
        await sharePage.waitForTimeout(1500);

        // Verify we can see the issue in read-only mode
        await expect(sharePage.locator("#detailHeader")).toContainText(
            "用于测试分享链接"
        );

        // Reply form should be hidden in share view
        await expect(sharePage.locator("#replyForm")).toBeHidden();

        // Cleanup
        await studentCtx.close();
        await shareCtx.close();
    });

    test("student Q&A button navigates to Q&A page", async ({ browser }) => {
        const seed = getSeedResult();

        const studentCtx = await browser.newContext();
        const studentPage = new StudentPage(await studentCtx.newPage());
        await studentPage.enterCode(seed.inviteCode);
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);
        await studentPage.page.reload();
        await studentPage.panel.waitFor({ state: "visible" });
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);

        // Start a homework session first
        await studentPage.startHomeworkSession(
            ASSIGNMENT_ID,
            "导航学生",
            "2024QA03",
            "导航班",
            "nav123"
        );

        // Verify Q&A button exists and shows "Q&A"
        const qaBtn = studentPage.page.locator("#qaToggleBtn");
        await expect(qaBtn).toContainText("Q&A");

        // Click should navigate to Q&A page
        await qaBtn.click();
        await studentPage.page.waitForURL("**/student/qa**", {
            timeout: 5000,
        });

        // Verify we're on the Q&A page
        await expect(studentPage.page.locator("h1")).toContainText("Q&A");

        await studentCtx.close();
    });

    test("teacher Q&A button navigates to teacher QA page", async ({
        browser,
    }) => {
        const seed = getSeedResult();

        const teacherCtx = await browser.newContext();
        const teacherPage = new TeacherPage(await teacherCtx.newPage());
        await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
        await teacherPage.selectCourse(seed.courseId);

        // Switch to homework sub-tab
        await teacherPage.switchSubTab("sub-homework");
        await teacherPage.page.waitForTimeout(500);

        // Click Q&A button
        const qaBtn = teacherPage.page.locator("#homeworkQABtn");
        await expect(qaBtn).toContainText("Q&A");
        await qaBtn.click();

        // Verify navigation to /teacher/qa
        await teacherPage.page.waitForURL("**/teacher/qa**", {
            timeout: 5000,
        });

        // Verify we're on the teacher QA page
        await expect(teacherPage.page.locator("#pageTitle")).toContainText("Q&A 管理");

        // Verify new issue button is hidden for teacher
        await expect(teacherPage.page.locator("#newIssueBtn")).toBeHidden();

        // Verify back button exists
        await expect(teacherPage.page.locator("#backBtn")).toContainText("返回教师面板");

        await teacherCtx.close();
    });

    test("teacher can hide and unhide issues", async ({ browser }) => {
        const seed = getSeedResult();

        // Create an issue via student API
        const studentCtx = await browser.newContext();
        const studentPage = new StudentPage(await studentCtx.newPage());
        await studentPage.enterCode(seed.inviteCode);
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);
        await studentPage.page.reload();
        await studentPage.panel.waitFor({ state: "visible" });
        await studentPage.switchTab("tab-homework");
        await studentPage.page.waitForTimeout(1000);

        await studentPage.startHomeworkSession(
            ASSIGNMENT_ID,
            "隐藏测试学生",
            "2024QA04",
            "隐藏测试班",
            "hide123"
        );

        const createRes = await studentPage.page.evaluate(
            async (courseId: number) => {
                const fd = new FormData();
                fd.append("course_id", String(courseId));
                fd.append("assignment_id", "e2e_qa_01");
                fd.append("message", "这是一个用于测试隐藏功能的问题");
                const r = await fetch("/api/qa/issue/create", {
                    method: "POST",
                    credentials: "include",
                    body: fd,
                });
                return r.json();
            },
            seed.courseId
        );
        expect(createRes.ok).toBe(true);

        // Teacher opens QA page
        const teacherCtx = await browser.newContext();
        const teacherPage = new TeacherPage(await teacherCtx.newPage());
        await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
        await teacherPage.page.goto(
            `/teacher/qa?course_id=${seed.courseId}&assignment_id=${ASSIGNMENT_ID}`
        );
        await teacherPage.page.waitForTimeout(2000);

        // Open the issue
        const issueItem = teacherPage.page.locator(".issue-list-item", {
            hasText: "测试隐藏功能",
        });
        await issueItem.click();
        await teacherPage.page.waitForTimeout(1000);

        // Hide the issue
        await teacherPage.page.locator("#teacherActions button:text('隐藏')").click();
        await teacherPage.page.waitForTimeout(1000);

        // Verify "取消隐藏" is now visible
        await expect(teacherPage.page.locator("#teacherActions button:text('取消隐藏')")).toBeVisible();

        // Go back to list and verify hidden badge
        await teacherPage.page.locator("button:text('返回列表')").click();
        await teacherPage.page.waitForTimeout(500);

        const hiddenItem = teacherPage.page.locator(".issue-list-item.hidden-item");
        await expect(hiddenItem.first()).toContainText("隐藏");

        // Cleanup
        await studentCtx.close();
        await teacherCtx.close();
    });
});