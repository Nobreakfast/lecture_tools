#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const net = require("net");
const { execSync, spawn } = require("child_process");
const { chromium } = require("@playwright/test");

const ROOT = path.resolve(__dirname, "../..");
const E2E_ROOT = path.resolve(__dirname, "..");
const BIN = path.join(ROOT, "bin", "server");
const MIGRATE_BIN = path.join(ROOT, "bin", "migrate");
const TMP = path.join(E2E_ROOT, ".tmp-docs");
const DATA_DIR = path.join(TMP, "data");
const METADATA_DIR = path.join(TMP, "metadata");
const QUIZ_ASSETS_DIR = path.join(TMP, "quiz_assets");
const DB_PATH = path.join(DATA_DIR, "app.db");
const SCREENSHOT_DIR = path.join(ROOT, "internal", "app", "web", "static", "docs", "screenshots");
const PORT = 19877;
const BASE_URL = `http://127.0.0.1:${PORT}`;

const ADMIN_ID = "docs_admin";
const ADMIN_NAME = "文档管理员";
const ADMIN_PASSWORD = "docs-admin-pwd";
const TEACHER_ID = "docs_teacher";
const TEACHER_NAME = "文档示例教师";
const TEACHER_PASSWORD = "docs-teacher-pwd";
const QUIZ_ID = "week_01";

let serverProcess = null;

function buildServer() {
  execSync("make build-server build-migrate", { cwd: ROOT, stdio: "inherit" });
}

function prepareTmpDirs() {
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(DATA_DIR, { recursive: true });
  fs.mkdirSync(METADATA_DIR, { recursive: true });
  fs.mkdirSync(QUIZ_ASSETS_DIR, { recursive: true });
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
}

function bootstrapAdmin() {
  execSync(
    [
      MIGRATE_BIN,
      "upgrade",
      `--db=${DB_PATH}`,
      `--metadata-dir=${METADATA_DIR}`,
      `--teacher-id=${ADMIN_ID}`,
      `--teacher-name=${JSON.stringify(ADMIN_NAME)}`,
      `--password=${JSON.stringify(ADMIN_PASSWORD)}`,
    ].join(" "),
    { cwd: ROOT, stdio: "inherit" }
  );
}

function startServer() {
  serverProcess = spawn(BIN, [], {
    cwd: ROOT,
    env: {
      ...process.env,
      APP_ADDR: `127.0.0.1:${PORT}`,
      DATA_DIR,
      METADATA_DIR,
      QUIZ_ASSETS_DIR,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  serverProcess.stdout.on("data", (buf) => process.stdout.write(buf));
  serverProcess.stderr.on("data", (buf) => process.stderr.write(buf));
}

function stopServer() {
  if (serverProcess && !serverProcess.killed) {
    serverProcess.kill("SIGTERM");
  }
  serverProcess = null;
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function checkPort(port) {
  return new Promise((resolve) => {
    const sock = new net.Socket();
    sock.setTimeout(500);
    sock.once("connect", () => {
      sock.destroy();
      resolve(true);
    });
    sock.once("error", () => {
      sock.destroy();
      resolve(false);
    });
    sock.once("timeout", () => {
      sock.destroy();
      resolve(false);
    });
    sock.connect(port, "127.0.0.1");
  });
}

async function waitForReady(timeoutMs = 15000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (await checkPort(PORT)) return;
    await wait(200);
  }
  throw new Error(`server not ready after ${timeoutMs}ms`);
}

async function apiPost(route, body, cookie) {
  const headers = { "Content-Type": "application/json" };
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(`${BASE_URL}${route}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  const setCookie = res.headers.get("set-cookie") || "";
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch (_) {
    json = { _raw: text };
  }
  return { status: res.status, json, setCookie, text };
}

async function uploadMultipart(route, fields, files, cookie) {
  const form = new FormData();
  for (const [key, value] of Object.entries(fields || {})) {
    form.append(key, String(value));
  }
  for (const file of files || []) {
    form.append(
      file.field,
      new Blob([fs.readFileSync(file.path)], { type: file.type }),
      file.name
    );
  }
  const headers = {};
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(`${BASE_URL}${route}`, {
    method: "POST",
    headers,
    body: form,
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch (_) {
    json = { _raw: text };
  }
  return { status: res.status, json, text, setCookie: res.headers.get("set-cookie") || "" };
}

function extractCookie(setCookie, name) {
  const match = String(setCookie || "").match(new RegExp(`${name}=([^;]+)`));
  if (!match) throw new Error(`cookie ${name} not found in ${setCookie}`);
  return `${name}=${match[1]}`;
}

async function seedDemoData() {
  const adminLogin = await apiPost("/api/auth/login", { id: ADMIN_ID, password: ADMIN_PASSWORD });
  if (adminLogin.status !== 200) throw new Error(`admin login failed: ${adminLogin.text}`);
  const adminCookie = extractCookie(adminLogin.setCookie, "auth_token");

  const createTeacher = await apiPost(
    "/api/admin/teachers",
    { id: TEACHER_ID, name: TEACHER_NAME, password: TEACHER_PASSWORD },
    adminCookie
  );
  if (createTeacher.status !== 200 && createTeacher.status !== 409) {
    throw new Error(`create teacher failed: ${createTeacher.text}`);
  }

  const teacherLogin = await apiPost("/api/auth/login", { id: TEACHER_ID, password: TEACHER_PASSWORD });
  if (teacherLogin.status !== 200) throw new Error(`teacher login failed: ${teacherLogin.text}`);
  const teacherCookie = extractCookie(teacherLogin.setCookie, "auth_token");

  const createCourse = await apiPost(
    "/api/teacher/courses",
    { name: "教师文档示例课", slug: "teacher-doc-demo" },
    teacherCookie
  );
  if (createCourse.status !== 200 && createCourse.status !== 409) {
    throw new Error(`create course failed: ${createCourse.text}`);
  }
  const course = createCourse.json.course;
  const courseId = Number(course.id);
  const inviteCode = course.invite_code;
  if (!courseId || !inviteCode) throw new Error(`missing course info: ${JSON.stringify(createCourse.json)}`);

  const quizYAML = fs.readFileSync(path.join(E2E_ROOT, "fixtures", "quiz.sample.yaml"), "utf-8");
  const saveQuizBank = await apiPost(
    `/api/teacher/courses/quiz-bank/save?course_id=${courseId}`,
    { quiz_id: QUIZ_ID, yaml: quizYAML, filename: `${QUIZ_ID}.yaml` },
    teacherCookie
  );
  if (saveQuizBank.status !== 200) throw new Error(`save quiz bank failed: ${saveQuizBank.text}`);

  const loadQuiz = await apiPost(
    `/api/teacher/courses/load-quiz?course_id=${courseId}`,
    { yaml: quizYAML },
    teacherCookie
  );
  if (loadQuiz.status !== 200) throw new Error(`load quiz failed: ${loadQuiz.text}`);

  const openEntry = await apiPost(
    `/api/teacher/courses/entry?course_id=${courseId}`,
    { open: true },
    teacherCookie
  );
  if (openEntry.status !== 200) throw new Error(`open entry failed: ${openEntry.text}`);

  const materialUpload = await uploadMultipart(
    `/api/teacher/courses/materials/upload?course_id=${courseId}`,
    { course_id: courseId },
    [
      { field: "files", path: path.join(E2E_ROOT, "fixtures", "sample.pdf"), name: "课程讲义.pdf", type: "application/pdf" },
      { field: "files", path: path.join(E2E_ROOT, "fixtures", "sample.txt"), name: "课堂说明.txt", type: "text/plain" },
    ],
    teacherCookie
  );
  if (materialUpload.status !== 200) throw new Error(`upload materials failed: ${materialUpload.text}`);

  const homeworkUpload = await uploadMultipart(
    `/api/teacher/courses/homework/assignments/upload?course_id=${courseId}`,
    { course_id: courseId, assignment_id: "task_1" },
    [
      { field: "files", path: path.join(E2E_ROOT, "fixtures", "sample.pdf"), name: "作业说明.pdf", type: "application/pdf" },
      { field: "files", path: path.join(E2E_ROOT, "fixtures", "sample.txt"), name: "readme.txt", type: "text/plain" },
    ],
    teacherCookie
  );
  if (homeworkUpload.status !== 200) throw new Error(`upload homework failed: ${homeworkUpload.text}`);

  const join = await apiPost("/api/join", {
    name: "张同学",
    student_no: "20260001",
    class_name: "人工智能1班",
    course_id: courseId,
  });
  if (join.status !== 200) throw new Error(`student join failed: ${join.text}`);
  const studentCookie = extractCookie(join.setCookie, "student_token");

  for (const answer of [
    { question_id: "q1", answer: "B" },
    { question_id: "q2", answer: "Y" },
    { question_id: "q3", answer: "A,B,C" },
    { question_id: "q5", answer: "对凸函数判定还不够熟悉" },
    { question_id: "q7", answer: "def solve():\n    return 'ok'" },
  ]) {
    const saved = await apiPost("/api/answer", answer, studentCookie);
    if (saved.status !== 200) throw new Error(`save answer failed: ${saved.text}`);
  }

  const submit = await apiPost("/api/submit", {}, studentCookie);
  if (submit.status !== 200) throw new Error(`submit failed: ${submit.text}`);

  const homeworkSession = await apiPost("/api/homework/session", {
    course_id: courseId,
    assignment_id: "task_1",
    name: "张同学",
    student_no: "20260001",
    class_name: "人工智能1班",
    secret_key: "DOCS-2026",
  });
  if (homeworkSession.status !== 200) throw new Error(`homework session failed: ${homeworkSession.text}`);
  const homeworkCookie = extractCookie(homeworkSession.setCookie, "homework_token");

  const uploadReport = await uploadMultipart(
    "/api/homework/upload",
    { slot: "report" },
    [
      { field: "file", path: path.join(E2E_ROOT, "fixtures", "sample.pdf"), name: "实验报告.pdf", type: "application/pdf" },
    ],
    homeworkCookie
  );
  if (uploadReport.status !== 200) throw new Error(`upload report failed: ${uploadReport.text}`);

  const uploadCode = await uploadMultipart(
    "/api/homework/upload",
    { slot: "code" },
    [
      { field: "file", path: path.join(E2E_ROOT, "fixtures", "sample.ipynb"), name: "solution.ipynb", type: "application/x-ipynb+json" },
    ],
    homeworkCookie
  );
  if (uploadCode.status !== 200) throw new Error(`upload code failed: ${uploadCode.text}`);

  return { courseId, inviteCode, quizId: QUIZ_ID };
}

async function takeScreenshots(seed) {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } });

  async function save(name, options) {
    const target = path.join(SCREENSHOT_DIR, name);
    await page.screenshot({ path: target, fullPage: true, ...options });
    console.log(`saved ${target}`);
  }

  await page.goto(`${BASE_URL}/teacher`);
  await page.fill("#loginId", TEACHER_ID);
  await page.fill("#loginPwd", TEACHER_PASSWORD);
  await page.click("#loginBtn");
  await page.waitForSelector("#view-main:not(.hidden)");
  await page.waitForTimeout(800);

  await save("teacher-dashboard.png");
  await save("teacher-course-and-invite.png");

  await page.click('.tab-btn[data-tab="tab-upload"]');
  await page.waitForTimeout(900);
  await save("teacher-quiz-upload.png");

  await page.click('.tab-btn[data-tab="tab-attempts"]');
  await page.waitForSelector("#attemptsList table");
  await page.waitForTimeout(900);
  await save("teacher-attempts.png");

  await page.click('.tab-btn[data-tab="tab-upload"]');
  await page.click('.sub-tab-btn[data-subtab="sub-materials"]');
  await page.waitForTimeout(900);
  await save("teacher-materials.png");

  await page.click('.sub-tab-btn[data-subtab="sub-homework"]');
  await page.waitForTimeout(900);
  await save("teacher-homework.png");

  await page.click('.tab-btn[data-tab="tab-summary"]');
  await page.waitForTimeout(1200);
  await save("teacher-summary.png");

  await page.goto(`${BASE_URL}/quiz-editor?course_id=${seed.courseId}&quiz_id=${encodeURIComponent(seed.quizId)}`);
  await page.waitForSelector("#editorRoot");
  await page.waitForTimeout(1200);
  await save("teacher-quiz-editor.png");

  await page.click('button:has-text("AI 生成")');
  await page.waitForSelector(".ai-panel-backdrop");
  await page.waitForTimeout(500);
  await save("teacher-quiz-ai.png");

  await browser.close();
}

async function main() {
  try {
    buildServer();
    prepareTmpDirs();
    bootstrapAdmin();
    startServer();
    await waitForReady();
    const seed = await seedDemoData();
    await takeScreenshots(seed);
  } finally {
    stopServer();
  }
}

main().catch((err) => {
  console.error(err);
  stopServer();
  process.exit(1);
});
