// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { test, expect } from "@playwright/test";
import { getSeedResult, TEACHER_ID, TEACHER_PASSWORD } from "../helpers/seed";
import { BASE_URL } from "../helpers/server";
import { TeacherPage } from "../pages/teacher.page";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

async function apiPost(
  path: string,
  body: Record<string, unknown>,
  cookie: string
): Promise<{ status: number; json: any }> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Cookie: cookie },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  let json: any;
  try { json = JSON.parse(text); } catch { json = { _raw: text }; }
  return { status: res.status, json };
}

async function apiGet(path: string, cookie?: string): Promise<{ status: number; json: any }> {
  const headers: Record<string, string> = {};
  if (cookie) headers["Cookie"] = cookie;
  const res = await fetch(`${BASE_URL}${path}`, { headers });
  const text = await res.text();
  let json: any;
  try { json = JSON.parse(text); } catch { json = { _raw: text }; }
  return { status: res.status, json };
}

async function apiDelete(path: string, cookie: string): Promise<{ status: number; json: any }> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: "DELETE",
    headers: { Cookie: cookie },
  });
  const text = await res.text();
  let json: any;
  try { json = JSON.parse(text); } catch { json = { _raw: text }; }
  return { status: res.status, json };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Quiz share", () => {
  let shareToken: string;
  let shareId: number;

  test("teacher creates a share link", async () => {
    const seed = getSeedResult();
    const res = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    expect(res.status).toBe(200);
    expect(typeof res.json.share_token).toBe("string");
    expect(res.json.share_token.length).toBeGreaterThan(0);
    expect(res.json.share_url).toContain("/share?token=");
    shareToken = res.json.share_token;
  });

  test("teacher lists active share links", async () => {
    const seed = getSeedResult();

    // Ensure at least one share exists for this course
    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    expect(createRes.status).toBe(200);
    const token = createRes.json.share_token as string;
    const quizId = createRes.json.quiz_id as string;

    const listRes = await apiGet(
      `/api/teacher/courses/share/list?course_id=${seed.courseId}&quiz_id=${quizId}`,
      seed.teacherCookie
    );
    expect(listRes.status).toBe(200);
    const items: any[] = listRes.json.items ?? [];
    const found = items.find((it) => it.share_token === token);
    expect(found).toBeDefined();
    expect(found.share_url).toContain("/share?token=");
    expect(typeof found.id).toBe("number");
    shareId = found.id;
  });

  test("share page is accessible without login", async ({ page }) => {
    const seed = getSeedResult();

    // Create a fresh share token for this test
    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    expect(createRes.status).toBe(200);
    const token = createRes.json.share_token as string;

    // Visit share page in a clean (unauthenticated) browser context
    await page.goto(`/share?token=${token}`);
    // Page should load successfully (not redirect to login)
    await expect(page).not.toHaveURL(/\/teacher/);
    // Content div should eventually become visible
    await page.locator("#content").waitFor({ state: "visible", timeout: 8000 });
  });

  test("share page shows quiz title and course name", async ({ page }) => {
    const seed = getSeedResult();

    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    const token = createRes.json.share_token as string;

    await page.goto(`/share?token=${token}`);
    await page.locator("#content").waitFor({ state: "visible", timeout: 8000 });

    const title = await page.locator("#quiz-title").textContent();
    expect((title ?? "").trim().length).toBeGreaterThan(0);

    const courseName = await page.locator("#course-name").textContent();
    expect((courseName ?? "").trim().length).toBeGreaterThan(0);
  });

  test("share detail API returns quiz and attempts data without login", async () => {
    const seed = getSeedResult();

    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    const token = createRes.json.share_token as string;

    // No cookie — unauthenticated access
    const detailRes = await apiGet(`/api/share?token=${token}`);
    expect(detailRes.status).toBe(200);
    expect(detailRes.json.quiz_id).toBeDefined();
    expect(detailRes.json.course_name).toBeDefined();
    expect(Array.isArray(detailRes.json.attempts)).toBe(true);
    expect(typeof detailRes.json.total).toBe("number");
  });

  test("invalid share token returns 404 for the page", async ({ page }) => {
    const response = await page.goto("/share?token=invalidtokenxyz");
    expect(response?.status()).toBe(404);
  });

  test("invalid token returns 404 from the API", async () => {
    const res = await apiGet("/api/share?token=invalidtokenxyz");
    expect(res.status).toBe(404);
  });

  test("missing token returns 400 from the API", async () => {
    const res = await apiGet("/api/share");
    expect(res.status).toBe(400);
  });

  test("teacher revokes a share link", async () => {
    const seed = getSeedResult();

    // Create a share to revoke
    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    expect(createRes.status).toBe(200);
    const token = createRes.json.share_token as string;
    const quizId = createRes.json.quiz_id as string;

    // Fetch the share ID from the list
    const listRes = await apiGet(
      `/api/teacher/courses/share/list?course_id=${seed.courseId}&quiz_id=${quizId}`,
      seed.teacherCookie
    );
    const items: any[] = listRes.json.items ?? [];
    const found = items.find((it) => it.share_token === token);
    expect(found).toBeDefined();

    const revokeRes = await apiDelete(
      `/api/teacher/courses/share?id=${found.id}&course_id=${seed.courseId}`,
      seed.teacherCookie
    );
    expect(revokeRes.status).toBe(200);
    expect(revokeRes.json.ok).toBe(true);
  });

  test("revoked share page returns 410", async ({ page }) => {
    const seed = getSeedResult();

    // Create then revoke
    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    const token = createRes.json.share_token as string;
    const quizId = createRes.json.quiz_id as string;

    // Get the ID from the list
    const listRes = await apiGet(
      `/api/teacher/courses/share/list?course_id=${seed.courseId}&quiz_id=${quizId}`,
      seed.teacherCookie
    );
    const items: any[] = listRes.json.items ?? [];
    const found = items.find((it) => it.share_token === token);
    expect(found).toBeDefined();

    await apiDelete(
      `/api/teacher/courses/share?id=${found.id}&course_id=${seed.courseId}`,
      seed.teacherCookie
    );

    // Share page should now return 410
    const response = await page.goto(`/share?token=${token}`);
    expect(response?.status()).toBe(410);
  });

  test("teacher B cannot revoke teacher A's share link", async () => {
    const seed = getSeedResult();

    // Teacher A creates a share
    const createRes = await apiPost(
      `/api/teacher/courses/share?course_id=${seed.courseId}`,
      {},
      seed.teacherCookie
    );
    const token = createRes.json.share_token as string;
    const quizId = createRes.json.quiz_id as string;

    // Get share ID
    const listRes = await apiGet(
      `/api/teacher/courses/share/list?course_id=${seed.courseId}&quiz_id=${quizId}`,
      seed.teacherCookie
    );
    const items: any[] = listRes.json.items ?? [];
    const found = items.find((it) => it.share_token === token);
    expect(found).toBeDefined();

    // Teacher B attempts to revoke (using their own course_id = seed.courseIdB but targeting share from courseId)
    const revokeRes = await apiDelete(
      `/api/teacher/courses/share?id=${found.id}&course_id=${seed.courseIdB}`,
      seed.teacherBCookie
    );
    // Should be forbidden (403) because the share belongs to courseId, not courseIdB
    expect(revokeRes.status).toBe(403);
  });

  test("teacher UI: share modal button is visible in attempts tab", async ({
    browser,
  }) => {
    const seed = getSeedResult();
    const ctx = await browser.newContext();
    const teacherPage = new TeacherPage(await ctx.newPage());
    await teacherPage.login(TEACHER_ID, TEACHER_PASSWORD);
    await teacherPage.selectCourse(seed.courseId);
    await teacherPage.switchTab("tab-attempts");

    await expect(teacherPage.page.locator("#shareQuizBtn")).toBeVisible();
    await ctx.close();
  });
});
