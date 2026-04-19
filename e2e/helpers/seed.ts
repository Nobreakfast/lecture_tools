// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import * as fs from "fs";
import * as path from "path";
import { BASE_URL, ADMIN_ID, ADMIN_PASSWORD } from "./server";

const TEACHER_ID = "e2e_teacher";
const TEACHER_NAME = "E2E教师";
const TEACHER_PASSWORD = "teacher-pwd";

const TEACHER_B_ID = "e2e_teacher_b";
const TEACHER_B_NAME = "E2E教师B";
const TEACHER_B_PASSWORD = "teacher-b-pwd";

export {
  TEACHER_ID, TEACHER_NAME, TEACHER_PASSWORD,
  TEACHER_B_ID, TEACHER_B_NAME, TEACHER_B_PASSWORD,
};

export interface SeedResult {
  adminCookie: string;
  teacherCookie: string;
  courseId: number;
  inviteCode: string;
  teacherBCookie: string;
  courseIdB: number;
  inviteCodeB: string;
}

let seedResult: SeedResult | null = null;

async function apiPost(
  path: string,
  body: Record<string, unknown>,
  cookie?: string
): Promise<{ status: number; json: any; setCookie?: string }> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (cookie) headers["Cookie"] = cookie;
  const res = await fetch(`${BASE_URL}${path}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  const setCookieHeader = res.headers.get("set-cookie") ?? undefined;
  let json: any;
  const text = await res.text();
  try {
    json = JSON.parse(text);
  } catch {
    json = { _raw: text };
  }
  return { status: res.status, json, setCookie: setCookieHeader };
}

function extractAuthCookie(setCookie: string | undefined): string {
  if (!setCookie) throw new Error("No set-cookie header");
  const match = setCookie.match(/auth_token=([^;]+)/);
  if (!match) throw new Error("auth_token not found in: " + setCookie);
  return `auth_token=${match[1]}`;
}

async function uploadQuizYAML(courseId: number, cookie: string): Promise<void> {
  const yamlPath = path.resolve(__dirname, "../fixtures/quiz.sample.yaml");
  const yamlContent = fs.readFileSync(yamlPath, "utf-8");
  const res = await apiPost(
    `/api/teacher/courses/load-quiz?course_id=${courseId}`,
    { yaml: yamlContent },
    cookie
  );
  if (res.status !== 200) {
    throw new Error(`Failed to load quiz: ${res.status} ${JSON.stringify(res.json)}`);
  }
}

export async function seedData(): Promise<SeedResult> {
  // 1. Admin login
  const adminLogin = await apiPost("/api/auth/login", {
    id: ADMIN_ID,
    password: ADMIN_PASSWORD,
  });
  if (adminLogin.status !== 200) {
    throw new Error(`Admin login failed: ${adminLogin.status} ${JSON.stringify(adminLogin.json)}`);
  }
  const adminCookie = extractAuthCookie(adminLogin.setCookie);

  // 2. Create teacher
  const createTeacher = await apiPost(
    "/api/admin/teachers",
    { id: TEACHER_ID, name: TEACHER_NAME, password: TEACHER_PASSWORD },
    adminCookie
  );
  if (createTeacher.status !== 200 && createTeacher.status !== 409) {
    throw new Error(`Create teacher failed: ${createTeacher.status}`);
  }

  // 3. Teacher login
  const teacherLogin = await apiPost("/api/auth/login", {
    id: TEACHER_ID,
    password: TEACHER_PASSWORD,
  });
  if (teacherLogin.status !== 200) {
    throw new Error(`Teacher login failed: ${teacherLogin.status}`);
  }
  const teacherCookie = extractAuthCookie(teacherLogin.setCookie);

  // 4. Create course
  const createCourse = await apiPost(
    "/api/teacher/courses",
    { name: "E2E测试课程", slug: "e2e-test" },
    teacherCookie
  );
  if (createCourse.status !== 200 && createCourse.status !== 409) {
    throw new Error(`Create course failed: ${createCourse.status}`);
  }
  const courseId: number = createCourse.json?.course?.id;
  const inviteCode: string = createCourse.json?.course?.invite_code;

  if (!courseId || !inviteCode) {
    throw new Error(
      `Missing course info from response: ${JSON.stringify(createCourse.json)}`
    );
  }

  // 5. Load quiz
  await uploadQuizYAML(courseId, teacherCookie);

  // 6. Create teacher B
  const createTeacherB = await apiPost(
    "/api/admin/teachers",
    { id: TEACHER_B_ID, name: TEACHER_B_NAME, password: TEACHER_B_PASSWORD },
    adminCookie
  );
  if (createTeacherB.status !== 200 && createTeacherB.status !== 409) {
    throw new Error(`Create teacher B failed: ${createTeacherB.status}`);
  }

  // 7. Teacher B login
  const teacherBLogin = await apiPost("/api/auth/login", {
    id: TEACHER_B_ID,
    password: TEACHER_B_PASSWORD,
  });
  if (teacherBLogin.status !== 200) {
    throw new Error(`Teacher B login failed: ${teacherBLogin.status}`);
  }
  const teacherBCookie = extractAuthCookie(teacherBLogin.setCookie);

  // 8. Create course B (same quiz_id to exercise shared-quiz isolation)
  const createCourseB = await apiPost(
    "/api/teacher/courses",
    { name: "E2E测试课程B", slug: "e2e-test-b" },
    teacherBCookie
  );
  if (createCourseB.status !== 200 && createCourseB.status !== 409) {
    throw new Error(`Create course B failed: ${createCourseB.status}`);
  }
  const courseIdB: number = createCourseB.json?.course?.id;
  const inviteCodeB: string = createCourseB.json?.course?.invite_code;
  if (!courseIdB || !inviteCodeB) {
    throw new Error(
      `Missing course B info: ${JSON.stringify(createCourseB.json)}`
    );
  }

  // 9. Load same quiz into course B
  await uploadQuizYAML(courseIdB, teacherBCookie);

  seedResult = {
    adminCookie, teacherCookie, courseId, inviteCode,
    teacherBCookie, courseIdB, inviteCodeB,
  };

  // Persist seed result so tests can read it
  const outPath = path.resolve(__dirname, "../.tmp/seed.json");
  fs.writeFileSync(outPath, JSON.stringify(seedResult, null, 2));

  return seedResult;
}

export function getSeedResult(): SeedResult {
  if (seedResult) return seedResult;
  const p = path.resolve(__dirname, "../.tmp/seed.json");
  if (!fs.existsSync(p)) {
    throw new Error("seed.json not found — did global-setup run?");
  }
  seedResult = JSON.parse(fs.readFileSync(p, "utf-8"));
  return seedResult!;
}
