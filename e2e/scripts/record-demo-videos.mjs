// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { chromium } from "@playwright/test";
import { execFileSync, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as net from "node:net";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "../..");
const TMP = path.join(ROOT, "e2e", ".tmp");
const VIDEO_TMP = path.join(TMP, "demo-video-raw");
const DATA_DIR = path.join(TMP, "data");
const METADATA_DIR = path.join(TMP, "metadata");
const QUIZ_ASSETS_DIR = path.join(TMP, "quiz_assets");
const SNAPSHOT_DIR = path.join(TMP, "snapshots");
const DB_PATH = path.join(DATA_DIR, "app.db");
const BIN = path.join(ROOT, "bin", "server");
const MIGRATE_BIN = path.join(ROOT, "bin", "migrate");
const PORT = 19876;
const BASE_URL = `http://127.0.0.1:${PORT}`;
const VIDEOS_DIR = path.join(ROOT, "Videos");

const ADMIN_ID = "e2e_admin";
const ADMIN_NAME = "E2E管理员";
const ADMIN_PASSWORD = "e2e-admin-pwd";
const TEACHER_ID = "e2e_teacher";
const TEACHER_NAME = "E2E教师";
const TEACHER_PASSWORD = "teacher-pwd";
const TEACHER_B_ID = "e2e_teacher_b";
const TEACHER_B_NAME = "E2E教师B";
const TEACHER_B_PASSWORD = "teacher-b-pwd";

const iPadPortrait = {
  viewport: { width: 1024, height: 1366 },
  recordVideo: { dir: VIDEO_TMP, size: { width: 1024, height: 1366 } },
  deviceScaleFactor: 1,
  isMobile: false,
  hasTouch: true,
};

let serverProcess;

const videoGroups = [
  {
    dir: "01_学生端_入场与随堂测验",
    title: "学生端：入场、答题、结果",
    videos: [
      ["01_输入邀请码进入课程.mp4", "输入邀请码，进入课程主页，确认课程和教师信息。"],
      ["02_开始随堂测验并提交.mp4", "教师开启入口后，学生填写身份信息、进入随堂测验、提交答案并查看结果。"],
    ],
  },
  {
    dir: "02_教师端_课程题库与入口控制",
    title: "教师端：课程、题库、入口控制",
    videos: [
      ["01_教师登录与课程列表.mp4", "教师登录后台，查看已创建课程与当前课程入口。"],
      ["02_题库上传与测验入口控制.mp4", "教师上传 YAML 题库，打开和关闭随堂测验入口。"],
    ],
  },
  {
    dir: "03_资料与作业",
    title: "资料与作业",
    videos: [
      ["01_教师发布资料与作业.mp4", "教师上传课程资料和作业说明，确认资源列表更新。"],
      ["02_学生查看资料并提交作业.mp4", "学生查看已发布资料，进入作业会话并上传 PDF 报告。"],
    ],
  },
  {
    dir: "04_成绩考勤与隐私",
    title: "成绩、考勤与隐私",
    videos: [
      ["01_成绩记录与考勤统计.mp4", "教师查看学生提交记录、实时统计与考勤表。"],
      ["02_隐私保护模式.mp4", "教师开启隐私保护，演示姓名和学号脱敏显示。"],
    ],
  },
  {
    dir: "05_系统管理员",
    title: "系统管理员",
    videos: [
      ["01_管理员概览与在线状态.mp4", "管理员查看教师、课程、学生、答题和在线状态概览。"],
      ["02_教师账号管理与AI配置检查.mp4", "管理员新增教师账号，并进入 AI 配置页执行健康检查。"],
    ],
  },
  {
    dir: "06_学生端_异常会话与题型",
    title: "学生端：异常、会话、短链、题型",
    videos: [
      ["01_无效邀请码与入口关闭等待.mp4", "演示无效邀请码报错，以及教师关闭入口时学生端等待状态。"],
      ["02_短链进入课程.mp4", "演示 /s/ 短链自动解析邀请码并进入课程。"],
      ["03_返回课程页_恢复答题_退出答题.mp4", "学生答题中返回课程页，可继续答题或退出当前答题会话。"],
      ["04_简答题文本图片代码组合模式.mp4", "展示文本、图片、代码、文本+图片四种简答题输入模式。"],
    ],
  },
  {
    dir: "07_教师端_高级课程与辅助工具",
    title: "教师端：高级课程与辅助工具",
    videos: [
      ["01_错误登录与课程创建规范化.mp4", "演示错误密码停留在登录页，以及英文课程名/全角字符规范化。"],
      ["02_教师使用文档与课堂数据助手.mp4", "打开教师使用文档和教师端课堂数据助手面板。"],
      ["03_课程邀请码二维码与删除课程.mp4", "演示课程二维码入口、临时课程创建与删除确认流程。"],
    ],
  },
  {
    dir: "08_题库_编辑器与分享",
    title: "题库、编辑器与分享",
    videos: [
      ["01_题库包上传与加载.mp4", "上传题库包并从题库列表加载为当前小测。"],
      ["02_题库编辑器预览.mp4", "打开题库编辑器，查看简答题不同模式的预览效果。"],
      ["03_分享小测与公开结果页.mp4", "教师创建分享链接，并用免登录公开分享页查看小测结果。"],
    ],
  },
  {
    dir: "09_成绩导出总结与MCP",
    title: "成绩导出、课堂总结与 MCP",
    videos: [
      ["01_答题详情与CSV导出.mp4", "查看学生答题详情，并演示 CSV 导出按钮。"],
      ["02_当前题库总结与历史趋势.mp4", "展示当前题库统计、历史趋势统计和 AI 总结入口。"],
      ["03_MCP长效Token配置.mp4", "在教师其它页开启长效 MCP Token，展示可复制配置。"],
    ],
  },
  {
    dir: "10_作业评分与Q&A",
    title: "作业评分与 Q&A",
    videos: [
      ["01_作业评分页_匿名_自动保存.mp4", "打开作业评分页，演示匿名评分、上下份导航和自动保存。"],
      ["02_成绩公布与学生查看.mp4", "教师公布作业成绩，学生端刷新后查看评分与评语。"],
      ["03_学生提问与教师回复.mp4", "学生创建 Q&A 问题，教师进入 Q&A 管理页回复。"],
      ["04_作业密钥错误与可见性控制.mp4", "演示作业密钥校验失败，以及教师侧作业可见性控制入口。"],
    ],
  },
  {
    dir: "11_管理员_安全运维",
    title: "管理员：安全与运维",
    videos: [
      ["01_管理员错误登录与教师权限隔离.mp4", "演示管理员错误登录，以及普通教师不能进入系统管理页。"],
      ["02_AI配置保存重新加载.mp4", "管理员保存 AI 配置并重新加载查看。"],
      ["03_系统快照生成与下载入口.mp4", "查看系统快照页，生成轻量快照并展示下载/恢复入口。"],
      ["04_系统更新安全提示.mp4", "查看系统更新页和升级窗口安全提示，不执行拉取或重启。"],
    ],
  },
];

function run(command, args, options = {}) {
  execFileSync(command, args, { cwd: ROOT, stdio: "inherit", ...options });
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function writeFile(file, body) {
  ensureDir(path.dirname(file));
  fs.writeFileSync(file, body, "utf8");
}

function cleanName(name) {
  return name.replace(/[\\/:*?"<>|]/g, "_");
}

async function wait(ms) {
  await new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForReady(timeoutMs = 15_000) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    if (await canConnect(PORT)) return;
    await wait(200);
  }
  throw new Error(`Server not ready after ${timeoutMs}ms`);
}

function canConnect(port) {
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

async function apiPost(apiPath, body, cookie) {
  const headers = { "Content-Type": "application/json" };
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(`${BASE_URL}${apiPath}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json, setCookie: res.headers.get("set-cookie") };
}

async function apiGet(apiPath, cookie) {
  const headers = {};
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(`${BASE_URL}${apiPath}`, { headers });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json, text };
}

async function apiDelete(apiPath, cookie) {
  const headers = {};
  if (cookie) headers.Cookie = cookie;
  const res = await fetch(`${BASE_URL}${apiPath}`, { method: "DELETE", headers });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json };
}

function authCookie(setCookie) {
  const match = String(setCookie || "").match(/auth_token=([^;]+)/);
  if (!match) throw new Error(`auth_token not found in ${setCookie}`);
  return `auth_token=${match[1]}`;
}

async function uploadQuizYAML(courseId, cookie) {
  const yaml = fs.readFileSync(path.join(ROOT, "e2e", "fixtures", "quiz.sample.yaml"), "utf8");
  const res = await apiPost(`/api/teacher/courses/load-quiz?course_id=${courseId}`, { yaml }, cookie);
  if (res.status !== 200) throw new Error(`load quiz failed: ${res.status} ${JSON.stringify(res.json)}`);
}

async function seedData() {
  const adminLogin = await apiPost("/api/auth/login", { id: ADMIN_ID, password: ADMIN_PASSWORD });
  const adminCookie = authCookie(adminLogin.setCookie);

  await apiPost("/api/admin/teachers", { id: TEACHER_ID, name: TEACHER_NAME, password: TEACHER_PASSWORD }, adminCookie);
  const teacherLogin = await apiPost("/api/auth/login", { id: TEACHER_ID, password: TEACHER_PASSWORD });
  const teacherCookie = authCookie(teacherLogin.setCookie);

  const course = await apiPost("/api/teacher/courses", { name: "E2E测试课程", slug: "e2e-test" }, teacherCookie);
  const courseId = course.json?.course?.id;
  const inviteCode = course.json?.course?.invite_code;
  if (!courseId || !inviteCode) throw new Error(`missing course info: ${JSON.stringify(course.json)}`);
  await uploadQuizYAML(courseId, teacherCookie);

  await apiPost("/api/admin/teachers", { id: TEACHER_B_ID, name: TEACHER_B_NAME, password: TEACHER_B_PASSWORD }, adminCookie);
  const teacherBLogin = await apiPost("/api/auth/login", { id: TEACHER_B_ID, password: TEACHER_B_PASSWORD });
  const teacherBCookie = authCookie(teacherBLogin.setCookie);
  const courseB = await apiPost("/api/teacher/courses", { name: "E2E测试课程B", slug: "e2e-test-b" }, teacherBCookie);
  await uploadQuizYAML(courseB.json.course.id, teacherBCookie);

  return {
    adminCookie,
    teacherCookie,
    courseId,
    inviteCode,
    teacherBCookie,
    courseIdB: courseB.json.course.id,
    inviteCodeB: courseB.json.course.invite_code,
  };
}

async function setup() {
  console.log("[demo] Building server");
  run("make", ["build-server", "build-migrate"]);

  console.log("[demo] Preparing isolated temp data");
  fs.rmSync(TMP, { recursive: true, force: true });
  ensureDir(DATA_DIR);
  ensureDir(METADATA_DIR);
  ensureDir(QUIZ_ASSETS_DIR);
  ensureDir(SNAPSHOT_DIR);
  ensureDir(VIDEO_TMP);

  console.log("[demo] Bootstrapping admin");
  run(MIGRATE_BIN, [
    "upgrade",
    `--db=${DB_PATH}`,
    `--metadata-dir=${METADATA_DIR}`,
    `--teacher-id=${ADMIN_ID}`,
    `--teacher-name=${ADMIN_NAME}`,
    `--password=${ADMIN_PASSWORD}`,
  ]);

  console.log("[demo] Starting server");
  serverProcess = spawn(BIN, [], {
    cwd: ROOT,
    env: {
      ...process.env,
      APP_ADDR: `127.0.0.1:${PORT}`,
      DATA_DIR,
      METADATA_DIR,
      QUIZ_ASSETS_DIR,
      SNAPSHOT_DIR,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  serverProcess.stdout.on("data", (d) => process.stdout.write(d));
  serverProcess.stderr.on("data", (d) => process.stderr.write(d));
  await waitForReady();

  console.log("[demo] Seeding demo data");
  const seed = await seedData();
  prepareReadmes();
  return seed;
}

function prepareReadmes() {
  ensureDir(VIDEOS_DIR);
  const top = [
    "# 课程助手比赛演示视频",
    "",
    "本目录保存使用 iPad 竖屏尺寸录制的功能演示视频。视频内包含底部文字浮层和点击高亮，说明当前测试点、点击位置和预期结果。",
    "",
    "## 目录说明",
    "",
    ...videoGroups.map((group) => `- \`${group.dir}/\`：${group.title}`),
    "",
  ].join("\n");
  writeFile(path.join(VIDEOS_DIR, "README.md"), top);

  for (const group of videoGroups) {
    const lines = [
      `# ${group.title}`,
      "",
      "本文件夹内的视频片段用于演示同一类功能，每段视频均使用 iPad 竖屏视口录制，并带有底部说明浮层、慢速节奏和点击高亮。",
      "",
      "## 视频说明",
      "",
      ...group.videos.map(([file, desc]) => `- \`${file}\`：${desc}`),
      "",
    ];
    writeFile(path.join(VIDEOS_DIR, group.dir, "README.md"), lines.join("\n"));
  }
}

async function showStep(page, title, detail, ms = 1800) {
  await page.evaluate(
    ({ title, detail }) => {
      const existing = document.getElementById("__demoOverlay");
      if (existing) existing.remove();
      const style = document.getElementById("__demoOverlayStyle") || document.createElement("style");
      style.id = "__demoOverlayStyle";
      style.textContent = `
        #__demoOverlay {
          position: fixed;
          left: 22px;
          right: 22px;
          bottom: 22px;
          z-index: 2147483647;
          padding: 18px 20px;
          border-radius: 14px;
          background: rgba(15, 23, 42, .92);
          color: white;
          font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
          box-shadow: 0 16px 42px rgba(15, 23, 42, .24);
          pointer-events: none;
          border: 1px solid rgba(255, 255, 255, .18);
        }
        #__demoOverlay strong { display: block; font-size: 22px; line-height: 1.25; margin-bottom: 6px; }
        #__demoOverlay span { display: block; font-size: 16px; line-height: 1.55; opacity: .94; }
        #__demoClickHighlight {
          position: fixed;
          z-index: 2147483646;
          border: 4px solid #f59e0b;
          border-radius: 14px;
          box-shadow: 0 0 0 8px rgba(245, 158, 11, .22), 0 18px 46px rgba(15, 23, 42, .2);
          pointer-events: none;
          animation: __demoPulse 1.1s ease-in-out infinite;
        }
        #__demoClickHighlight::after {
          content: attr(data-label);
          position: absolute;
          left: 50%;
          top: -38px;
          transform: translateX(-50%);
          max-width: 340px;
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
          padding: 6px 10px;
          border-radius: 999px;
          background: rgba(245, 158, 11, .96);
          color: #111827;
          font-size: 14px;
          font-weight: 700;
          box-shadow: 0 10px 22px rgba(15, 23, 42, .16);
        }
        @keyframes __demoPulse {
          0%, 100% { transform: scale(1); opacity: 1; }
          50% { transform: scale(1.03); opacity: .72; }
        }
      `;
      if (!style.parentElement) document.head.appendChild(style);
      const overlay = document.createElement("div");
      overlay.id = "__demoOverlay";
      overlay.innerHTML = `<strong>${title}</strong><span>${detail}</span>`;
      document.body.appendChild(overlay);
    },
    { title, detail },
  );
  await page.waitForTimeout(ms);
}

async function highlightTarget(page, locator, label = "点击这里") {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  if (!box) return;
  await page.evaluate(
    ({ box, label }) => {
      const existing = document.getElementById("__demoClickHighlight");
      if (existing) existing.remove();
      const marker = document.createElement("div");
      marker.id = "__demoClickHighlight";
      marker.dataset.label = label;
      marker.style.left = `${Math.max(8, box.x - 8)}px`;
      marker.style.top = `${Math.max(8, box.y - 8)}px`;
      marker.style.width = `${box.width + 16}px`;
      marker.style.height = `${box.height + 16}px`;
      document.body.appendChild(marker);
    },
    { box, label },
  );
  await page.waitForTimeout(950);
}

async function clickHighlighted(page, locator, label = "点击这里") {
  await highlightTarget(page, locator, label);
  await locator.click();
  await page.waitForTimeout(1050);
}

async function selectHighlighted(page, locator, value, label = "选择这里") {
  await highlightTarget(page, locator, label);
  await locator.selectOption(value);
  await page.waitForTimeout(1050);
}

async function fillHighlighted(page, locator, value, label = "填写这里") {
  await highlightTarget(page, locator, label);
  await locator.fill(value);
  await page.waitForTimeout(650);
}

async function saveRecording(page, context, destMp4) {
  const video = page.video();
  await context.close();
  const webm = destMp4.replace(/\.mp4$/, ".webm");
  await video.saveAs(webm);
  run("ffmpeg", ["-y", "-i", webm, "-vf", "fps=60", "-c:v", "libx264", "-preset", "slow", "-crf", "16", "-pix_fmt", "yuv420p", "-movflags", "+faststart", destMp4], {
    stdio: "ignore",
  });
}

async function record(browser, groupDir, fileName, scenario) {
  const output = path.join(VIDEOS_DIR, groupDir, fileName);
  ensureDir(path.dirname(output));
  console.log(`[demo] Recording ${path.relative(ROOT, output)}`);
  const context = await browser.newContext(iPadPortrait);
  const page = await context.newPage();
  page.setDefaultTimeout(12_000);
  await scenario(page);
  await page.waitForTimeout(1200);
  await saveRecording(page, context, output);
}

async function loginTeacher(page) {
  await page.goto(`${BASE_URL}/teacher`);
  await showStep(page, "教师登录", "输入教师账号和密码，进入教师后台。");
  await fillHighlighted(page, page.locator("#loginId"), TEACHER_ID, "填写教师账号");
  await fillHighlighted(page, page.locator("#loginPwd"), TEACHER_PASSWORD, "填写教师密码");
  await clickHighlighted(page, page.locator("#loginBtn"), "点击登录");
  await page.locator("#view-main").waitFor({ state: "visible" });
}

async function loginAdmin(page) {
  await page.goto(`${BASE_URL}/admin`);
  await showStep(page, "管理员登录", "输入管理员账号和密码，进入系统管理后台。");
  await fillHighlighted(page, page.locator("#loginId"), ADMIN_ID, "填写管理员账号");
  await fillHighlighted(page, page.locator("#loginPwd"), ADMIN_PASSWORD, "填写管理员密码");
  await clickHighlighted(page, page.locator("#loginBtn"), "点击登录");
  await page.locator("#view-main").waitFor({ state: "visible" });
}

async function selectCourse(page, courseId) {
  await clickHighlighted(page, page.locator(`.course-pill[data-course-id="${courseId}"]`), "选择当前课程");
}

async function switchTeacherTab(page, tab) {
  await clickHighlighted(page, page.locator(`.tab-btn[data-tab="${tab}"]`), "切换功能页");
  await page.locator(`#${tab}`).waitFor({ state: "visible" });
}

async function switchStudentTab(page, tab) {
  await clickHighlighted(page, page.locator(`.tab-btn[data-tab="${tab}"]`), "切换学生功能页");
  await page.locator(`#${tab}`).waitFor({ state: "visible" });
}

async function openEntry(seed, open) {
  await apiPost(`/api/teacher/courses/entry?course_id=${seed.courseId}`, { open }, seed.teacherCookie);
}

async function joinStudentQuiz(page, seed, studentName, studentNo) {
  await openEntry(seed, true);
  await page.goto(`${BASE_URL}/`);
  await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入课程邀请码");
  await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
  await page.locator("#panel").waitFor({ state: "visible" });
  await switchStudentTab(page, "tab-quiz");
  await page.locator("#quizForm").waitFor({ state: "visible" });
  await fillHighlighted(page, page.locator("#qz_name"), studentName, "填写姓名");
  await fillHighlighted(page, page.locator("#qz_student_no"), studentNo, "填写学号");
  await fillHighlighted(page, page.locator("#qz_class_name"), "比赛演示班", "填写班级");
  await clickHighlighted(page, page.locator("#quizJoinBtn"), "开始答题");
  await page.waitForURL("**/quiz", { waitUntil: "domcontentloaded" });
  await page.locator("#questions .card").first().waitFor({ state: "visible" });
}

async function answerQuiz(page) {
  await showStep(page, "答题演示", "点击选择题选项，并填写简答题内容。");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "Go 中用于定义结构体" }).locator("button").filter({ hasText: "struct" }), "选择正确选项 struct");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "是否理解了今天" }).locator("button").filter({ hasText: "是" }), "选择“是”");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "凸函数判定方法" }).locator("button").filter({ hasText: "定义不等式法" }), "选择定义不等式法");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "凸函数判定方法" }).locator("button").filter({ hasText: "一阶条件" }), "选择一阶条件");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "凸函数判定方法" }).locator("button").filter({ hasText: "二阶条件" }), "选择二阶条件");
  await clickHighlighted(page, page.locator(".card").filter({ hasText: "下节课增加" }).locator("button").filter({ hasText: "更多案例" }), "选择问卷选项");
  await fillHighlighted(page, page.locator(".card").filter({ hasText: "最困惑的知识点" }).locator("textarea"), "目前最困惑的是凸函数判定方法之间的联系。", "填写简答题");
  await fillHighlighted(page, page.locator(".card").filter({ hasText: "请粘贴你的实现代码" }).locator("textarea"), "func main() { fmt.Println(\"demo\") }", "填写代码题");
  await fillHighlighted(page, page.locator(".card").filter({ hasText: "解题思路" }).locator("textarea"), "已完成思路整理，稍后补充截图。", "填写思路题");
  await showStep(page, "提交测验", "点击底部提交按钮，查看自动判分和答题反馈。");
  await clickHighlighted(page, page.locator("#submit"), "提交测验");
  await page.waitForURL("**/result");
  await page.locator("#title").waitFor({ state: "visible" });
}

async function createStudentSubmission(seed, suffix = Date.now()) {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();
  await joinStudentQuiz(page, seed, `演示学生${suffix}`, `DEMO${suffix}`);
  await answerQuiz(page);
  await context.close();
  await browser.close();
}

async function publishMaterialAndHomework(page, seed, assignmentId) {
  await loginTeacher(page);
  await selectCourse(page, seed.courseId);
  await switchTeacherTab(page, "tab-upload");
  await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-materials"]'), "切换到课程资料");
  await page.locator("#sub-materials").waitFor({ state: "visible" });
  await showStep(page, "上传课程资料", "选择示例资料文件，发布给学生查看。");
  await page.locator("#materialFile").setInputFiles(path.join(ROOT, "e2e", "fixtures", "sample.txt"));
  await clickHighlighted(page, page.locator("#uploadMaterialBtn"), "上传资料");
  await page.locator("#materialList").waitFor({ state: "visible" });
  await page.waitForTimeout(1500);

  await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-homework"]'), "切换到作业管理");
  await page.locator("#sub-homework").waitFor({ state: "visible" });
  await showStep(page, "发布作业", "填写作业编号，上传作业说明文件。");
  await fillHighlighted(page, page.locator("#homeworkAssignmentIdInput"), assignmentId, "填写作业编号");
  await page.locator("#homeworkAssignmentFiles").setInputFiles(path.join(ROOT, "e2e", "fixtures", "sample.txt"));
  await clickHighlighted(page, page.locator("#uploadHomeworkBtn"), "发布作业");
  await page.waitForTimeout(1700);
}

async function createStudentSubmissionFast(seed, suffix = Date.now(), answers = {}) {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();
  await openEntry(seed, true);
  await page.goto(`${BASE_URL}/`);
  await page.locator("#codeInput").fill(seed.inviteCode);
  await page.locator("#codeBtn").click();
  await page.locator("#panel").waitFor({ state: "visible" });
  await page.locator('.tab-btn[data-tab="tab-quiz"]').click();
  await page.locator("#quizForm").waitFor({ state: "visible" });
  await page.locator("#qz_name").fill(`演示学生${suffix}`);
  await page.locator("#qz_student_no").fill(`DEMO${suffix}`);
  await page.locator("#qz_class_name").fill("比赛演示班");
  await page.locator("#quizJoinBtn").click();
  await page.waitForURL("**/quiz", { waitUntil: "domcontentloaded" });
  await page.locator("#questions .card").first().waitFor({ state: "visible" });
  const finalAnswers = {
    q1: "B",
    q2: "Y",
    q3: "A,B,C",
    q4: "A",
    q5: "用于准备演示数据。",
    ...answers,
  };
  await page.evaluate(async (payload) => {
    for (const [question_id, answer] of Object.entries(payload)) {
      await fetch("/api/answer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ question_id, answer }),
      });
    }
  }, finalAnswers);
  await page.locator("#submit").click();
  await page.waitForURL("**/result");
  await context.close();
  await browser.close();
}

async function createHomeworkSubmissionFast(seed, assignmentId, studentName, studentNo, secret) {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${BASE_URL}/`);
  await page.locator("#codeInput").fill(seed.inviteCode);
  await page.locator("#codeBtn").click();
  await page.locator("#panel").waitFor({ state: "visible" });
  await page.locator('.tab-btn[data-tab="tab-homework"]').click();
  await page.locator("#assignment_id").selectOption(assignmentId);
  await page.locator("#hw_name").fill(studentName);
  await page.locator("#hw_student_no").fill(studentNo);
  await page.locator("#hw_class_name").fill("评分班");
  await page.locator("#hw_secret_key").fill(secret);
  await page.locator("#enterBtn").click();
  await page.locator("#submissionCard").waitFor({ state: "visible" });
  await page.locator("#reportInput").setInputFiles(path.join(ROOT, "e2e", "fixtures", "sample.pdf"));
  await page.waitForTimeout(1000);
  await context.close();
  await browser.close();
}

async function publishHomeworkAssignmentFast(seed, assignmentId) {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();
  await loginTeacherFast(page);
  await page.locator(`.course-pill[data-course-id="${seed.courseId}"]`).click();
  await page.locator('.tab-btn[data-tab="tab-upload"]').click();
  await page.locator('.sub-tab-btn[data-subtab="sub-homework"]').click();
  await page.locator("#sub-homework").waitFor({ state: "visible" });
  await page.locator("#homeworkAssignmentIdInput").fill(assignmentId);
  await page.locator("#homeworkAssignmentFiles").setInputFiles(path.join(ROOT, "e2e", "fixtures", "sample.txt"));
  await page.locator("#uploadHomeworkBtn").click();
  await page.waitForTimeout(900);
  await context.close();
  await browser.close();
}

async function loginTeacherFast(page, id = TEACHER_ID, password = TEACHER_PASSWORD) {
  await page.goto(`${BASE_URL}/teacher`);
  await page.locator("#loginId").fill(id);
  await page.locator("#loginPwd").fill(password);
  await page.locator("#loginBtn").click();
  await page.locator("#view-main").waitFor({ state: "visible" });
}

async function loginAdminFast(page) {
  await page.goto(`${BASE_URL}/admin`);
  await page.locator("#loginId").fill(ADMIN_ID);
  await page.locator("#loginPwd").fill(ADMIN_PASSWORD);
  await page.locator("#loginBtn").click();
  await page.locator("#view-main").waitFor({ state: "visible" });
}

async function switchAdminTab(page, tab) {
  await clickHighlighted(page, page.locator(`.tab-btn[data-tab="${tab}"]`), "切换管理功能页");
  await page.locator(`.tab-page[data-tab="${tab}"]`).waitFor({ state: "visible" });
}

async function createShare(seed) {
  const res = await apiPost(`/api/teacher/courses/share?course_id=${seed.courseId}`, {}, seed.teacherCookie);
  if (res.status !== 200) throw new Error(`create share failed: ${res.status}`);
  return res.json;
}

async function main() {
  const seed = await setup();
  const browser = await chromium.launch({ slowMo: 180 });
  try {
    await record(browser, "01_学生端_入场与随堂测验", "01_输入邀请码进入课程.mp4", async (page) => {
      await page.goto(`${BASE_URL}/`);
      await showStep(page, "学生入口", "输入 6 位邀请码，进入对应课程。");
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入课程邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await showStep(page, "课程主页", "确认课程名称、教师信息，并查看学生端三个功能入口。", 2200);
      await switchStudentTab(page, "tab-materials");
      await showStep(page, "资料页", "资料 Tab 用于查看教师发布的课程资料。");
      await switchStudentTab(page, "tab-homework");
      await showStep(page, "作业页", "作业 Tab 用于进入作业会话和上传报告。");
    });

    await record(browser, "01_学生端_入场与随堂测验", "02_开始随堂测验并提交.mp4", async (page) => {
      await joinStudentQuiz(page, seed, "比赛演示学生", "DEMO001");
      await showStep(page, "开始随堂测验", "学生身份信息提交后，进入题目页面。");
      await answerQuiz(page);
      await showStep(page, "结果页", "系统展示得分、正确情况和反馈。", 1400);
    });

    await record(browser, "02_教师端_课程题库与入口控制", "01_教师登录与课程列表.mp4", async (page) => {
      await loginTeacher(page);
      await showStep(page, "教师主界面", "顶部显示教师身份，课程胶囊可快速切换课程。");
      await switchTeacherTab(page, "tab-courses");
      await showStep(page, "课程管理", "查看课程列表、邀请码和课程内部名称。", 1200);
      await selectCourse(page, seed.courseId);
    });

    await record(browser, "02_教师端_课程题库与入口控制", "02_题库上传与测验入口控制.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await showStep(page, "上传题库", "选择 YAML 题库文件并导入当前课程。");
      await page.locator("#yamlFileInput").setInputFiles(path.join(ROOT, "e2e", "fixtures", "quiz.sample.yaml"));
      await clickHighlighted(page, page.locator("#uploadYamlBtn"), "上传题库");
      await page.waitForTimeout(1600);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "打开测验入口", "点击打开入口后，学生即可开始答题。");
      await clickHighlighted(page, page.locator("#openEntryBtn"), "打开入口");
      await page.waitForTimeout(1300);
      await showStep(page, "关闭测验入口", "课堂结束时可关闭入口，阻止新的学生进入。");
      await clickHighlighted(page, page.locator("#closeEntryBtn"), "关闭入口");
      await page.waitForTimeout(1300);
    });

    const assignmentId = `demo_hw_${Date.now()}`;
    await record(browser, "03_资料与作业", "01_教师发布资料与作业.mp4", async (page) => {
      await publishMaterialAndHomework(page, seed, assignmentId);
      await showStep(page, "发布完成", "资料列表和作业列表已显示新发布内容。", 1200);
    });

    await record(browser, "03_资料与作业", "02_学生查看资料并提交作业.mp4", async (page) => {
      await page.goto(`${BASE_URL}/`);
      await showStep(page, "学生进入课程", "使用邀请码进入课程后，切换到资料和作业。");
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入课程邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await switchStudentTab(page, "tab-materials");
      await showStep(page, "查看资料", "学生可以看到教师发布且可见的课程资料。", 2200);
      await switchStudentTab(page, "tab-homework");
      await showStep(page, "进入作业会话", "选择作业，填写姓名、学号、班级和密钥。");
      await selectHighlighted(page, page.locator("#assignment_id"), assignmentId, "选择作业");
      await fillHighlighted(page, page.locator("#hw_name"), "作业演示学生", "填写姓名");
      await fillHighlighted(page, page.locator("#hw_student_no"), "HW001", "填写学号");
      await fillHighlighted(page, page.locator("#hw_class_name"), "比赛演示班", "填写班级");
      await fillHighlighted(page, page.locator("#hw_secret_key"), "secret123", "填写密钥");
      await clickHighlighted(page, page.locator("#enterBtn"), "进入作业");
      await page.locator("#submissionCard").waitFor({ state: "visible" });
      await showStep(page, "上传报告", "选择 PDF 报告文件，系统保存本次作业提交。");
      await page.locator("#reportInput").setInputFiles(path.join(ROOT, "e2e", "fixtures", "sample.pdf"));
      await page.waitForTimeout(2600);
    });

    await createStudentSubmissionFast(seed, "STAT1");
    await record(browser, "04_成绩考勤与隐私", "01_成绩记录与考勤统计.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "成绩记录", "查看实时开始人数、提交人数和学生答题记录。", 1300);
      await switchTeacherTab(page, "tab-attendance");
      await showStep(page, "考勤统计", "教师可按参与或提交维度查看课堂考勤。", 2200);
      await selectHighlighted(page, page.locator("#attendanceMode"), "submitted", "切换提交维度");
      await page.waitForTimeout(1500);
    });

    await record(browser, "04_成绩考勤与隐私", "02_隐私保护模式.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "原始记录", "默认显示教师名、学生姓名和学号。", 2200);
      await showStep(page, "开启隐私保护", "点击隐私保护后，身份信息会自动脱敏。");
      await clickHighlighted(page, page.getByRole("button", { name: "隐私保护" }), "开启隐私保护");
      await page.waitForTimeout(1700);
      await showStep(page, "脱敏显示", "姓名和学号已被遮罩，适合公开展示或投屏。", 2400);
    });

    await record(browser, "05_系统管理员", "01_管理员概览与在线状态.mp4", async (page) => {
      await loginAdmin(page);
      await showStep(page, "系统概览", "查看教师、课程、学生、答题数量和在线状态。", 2400);
      await clickHighlighted(page, page.locator('.tab-btn[data-tab="overview"]'), "查看系统概览");
      await page.waitForTimeout(1700);
    });

    await record(browser, "05_系统管理员", "02_教师账号管理与AI配置检查.mp4", async (page) => {
      await loginAdmin(page);
      await clickHighlighted(page, page.locator('.tab-btn[data-tab="teachers"]'), "进入教师管理");
      await page.locator('.tab-page[data-tab="teachers"]').waitFor({ state: "visible" });
      await showStep(page, "教师账号管理", "新增临时教师账号，管理员可统一管理教师。");
      const id = `demo_t_${Date.now()}`;
      await fillHighlighted(page, page.locator("#newTeacherId"), id, "填写教师账号");
      await fillHighlighted(page, page.locator("#newTeacherName"), "比赛演示教师", "填写教师姓名");
      await fillHighlighted(page, page.locator("#newTeacherPwd"), "demo-pwd-123", "填写初始密码");
      await clickHighlighted(page, page.locator("#createTeacherBtn"), "新增教师");
      await page.waitForTimeout(1700);
      await clickHighlighted(page, page.locator('.tab-btn[data-tab="ai"]'), "进入 AI 配置");
      await page.locator('.tab-page[data-tab="ai"]').waitFor({ state: "visible" });
      await showStep(page, "AI 配置检查", "进入 AI 配置页，点击健康检查查看当前配置状态。");
      await clickHighlighted(page, page.locator("#aiHealthBtn"), "执行健康检查");
      await page.waitForTimeout(2400);
    });

    await record(browser, "06_学生端_异常会话与题型", "01_无效邀请码与入口关闭等待.mp4", async (page) => {
      await openEntry(seed, false);
      await page.goto(`${BASE_URL}/`);
      await showStep(page, "无效邀请码", "先输入不存在的邀请码，确认学生端会给出错误提示。");
      await fillHighlighted(page, page.locator("#codeInput"), "ZZZZZZ", "输入错误邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "尝试进入");
      await page.waitForTimeout(1600);
      await showStep(page, "入口关闭等待", "再输入真实邀请码。教师未开启入口时，学生只能看到等待提示。");
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入真实邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await switchStudentTab(page, "tab-quiz");
      await page.locator("#quizWait").waitFor({ state: "visible", timeout: 10_000 });
      await showStep(page, "等待教师开启", "入口关闭时不会出现答题表单，避免学生提前进入。", 2600);
    });

    await record(browser, "06_学生端_异常会话与题型", "02_短链进入课程.mp4", async (page) => {
      await showStep(page, "短链入口", "访问 /s/邀请码，系统自动跳转并识别课程。");
      await page.goto(`${BASE_URL}/s/${seed.inviteCode}`);
      await page.waitForURL("**/?code=*", { timeout: 8000 });
      await page.locator("#panel").waitFor({ state: "visible" });
      await showStep(page, "自动解析成功", "短链适合二维码和课堂投屏，学生无需手动输入邀请码。", 2600);
    });

    await record(browser, "06_学生端_异常会话与题型", "03_返回课程页_恢复答题_退出答题.mp4", async (page) => {
      await joinStudentQuiz(page, seed, "恢复演示学生", "RESUME001");
      await showStep(page, "答题中返回", "学生可从测验页返回课程主页，暂存当前答题会话。");
      await clickHighlighted(page, page.locator("#exitBtn"), "返回课程页");
      await page.waitForURL("**/?stay=quiz", { timeout: 10_000 });
      await page.locator("#panel").waitFor({ state: "visible" });
      await page.locator("#quizResume").waitFor({ state: "visible" });
      await showStep(page, "恢复或退出", "课程页提供继续答题和退出答题两个选择。", 2200);
      await clickHighlighted(page, page.locator("#quizResumeBtn"), "继续答题");
      await page.waitForURL("**/quiz", { timeout: 10_000 });
      await page.locator("#questions .card").first().waitFor({ state: "visible" });
      await showStep(page, "主动退出", "如果学生放弃本次答题，可以退出当前答题会话。");
      await clickHighlighted(page, page.locator("#quitBtn"), "退出答题");
      await clickHighlighted(page, page.locator(".modal .btn").filter({ hasText: "退出答题" }), "确认退出");
      await page.waitForURL("**/", { timeout: 10_000 });
      await page.locator("#panel").waitFor({ state: "visible" });
      await showStep(page, "会话已清除", "退出后回到课程页，可重新开始新的答题。", 2400);
    });

    await record(browser, "06_学生端_异常会话与题型", "04_简答题文本图片代码组合模式.mp4", async (page) => {
      await joinStudentQuiz(page, seed, "题型演示学生", "MODE001");
      await showStep(page, "简答题模式", "向下滚动查看文本、图片、代码、文本+图片四类简答题。");
      await highlightTarget(page, page.locator(".card").filter({ hasText: "请描述你目前最困惑的知识点" }), "文本简答");
      await page.waitForTimeout(1600);
      await highlightTarget(page, page.locator(".card").filter({ hasText: "请上传你的代码运行截图" }), "图片上传");
      await page.waitForTimeout(1600);
      await highlightTarget(page, page.locator(".card").filter({ hasText: "请粘贴你的实现代码" }), "代码文本");
      await page.waitForTimeout(1600);
      await highlightTarget(page, page.locator(".card").filter({ hasText: "请描述你的解题思路并上传结果截图" }), "文本 + 图片");
      await page.waitForTimeout(2200);
    });

    await record(browser, "07_教师端_高级课程与辅助工具", "01_错误登录与课程创建规范化.mp4", async (page) => {
      await page.goto(`${BASE_URL}/teacher`);
      await showStep(page, "错误登录", "错误账号或密码不会进入教师后台。");
      await fillHighlighted(page, page.locator("#loginId"), "nonexistent", "填写错误账号");
      await fillHighlighted(page, page.locator("#loginPwd"), "wrongpwd", "填写错误密码");
      await clickHighlighted(page, page.locator("#loginBtn"), "尝试登录");
      await page.waitForTimeout(1800);
      await showStep(page, "正确登录", "使用教师账号进入后台后，演示课程名规范化。");
      await fillHighlighted(page, page.locator("#loginId"), TEACHER_ID, "填写教师账号");
      await fillHighlighted(page, page.locator("#loginPwd"), TEACHER_PASSWORD, "填写教师密码");
      await clickHighlighted(page, page.locator("#loginBtn"), "登录");
      await page.locator("#view-main").waitFor({ state: "visible" });
      await switchTeacherTab(page, "tab-courses");
      const suffix = Date.now().toString().slice(-4);
      await fillHighlighted(page, page.locator("#newCourseName"), `英文规范课程${suffix}`, "课程名称");
      await fillHighlighted(page, page.locator("#newCourseSlug"), "Machine Learning Intro", "英文展示名");
      await clickHighlighted(page, page.locator("#createCourseBtn"), "创建课程");
      await page.waitForTimeout(1800);
      await showStep(page, "规范化结果", "英文展示名保留空格，内部目录名自动转换为下划线。", 2600);
    });

    await record(browser, "07_教师端_高级课程与辅助工具", "02_教师使用文档与课堂数据助手.mp4", async (page) => {
      await loginTeacher(page);
      await showStep(page, "教师使用文档", "打开独立文档页，比赛演示时可作为操作说明。");
      const popupPromise = page.waitForEvent("popup");
      await clickHighlighted(page, page.getByRole("button", { name: "使用文档" }), "打开使用文档");
      const docsPage = await popupPromise;
      await docsPage.waitForLoadState("domcontentloaded");
      await showStep(docsPage, "文档页", "教师使用文档支持单独打开，对照后台操作。", 2600);
      await docsPage.close();
      await showStep(page, "课堂数据助手", "右下角机器人按钮可打开只读课堂数据助手。");
      await clickHighlighted(page, page.locator("#agentLauncher"), "打开助手");
      await page.locator("#agentPanel").waitFor({ state: "visible" });
      await fillHighlighted(page, page.locator("#agentInput"), "@", "输入 @ 查看可引用对象");
      await page.waitForTimeout(2600);
    });

    await record(browser, "07_教师端_高级课程与辅助工具", "03_课程邀请码二维码与删除课程.mp4", async (page) => {
      await loginTeacher(page);
      await switchTeacherTab(page, "tab-courses");
      await showStep(page, "课程二维码", "每门课程都可以下载邀请码二维码，便于学生扫码进入。");
      await highlightTarget(page, page.locator(`#courseCard_${seed.courseId}`).getByText("二维码"), "二维码下载入口");
      await page.waitForTimeout(2200);
      const name = `待删演示课程${Date.now().toString().slice(-4)}`;
      await showStep(page, "临时课程删除", "创建临时课程后演示删除确认，避免误删正式课程。");
      await fillHighlighted(page, page.locator("#newCourseName"), name, "课程名称");
      await fillHighlighted(page, page.locator("#newCourseSlug"), `delete demo ${Date.now()}`, "课程英文名");
      await clickHighlighted(page, page.locator("#createCourseBtn"), "创建临时课程");
      await page.waitForTimeout(1400);
      const card = page.locator(".course-card").filter({ hasText: name });
      await clickHighlighted(page, card.getByText("删除"), "删除临时课程");
      await clickHighlighted(page, page.locator("#confirmOkBtn"), "确认删除");
      await page.waitForTimeout(1800);
    });

    await record(browser, "08_题库_编辑器与分享", "01_题库包上传与加载.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-quiz"]'), "小测题库");
      await showStep(page, "上传题库包", "上传另一个 YAML 题库，题库列表会显示可加载的小测。");
      await page.locator("#yamlFileInput").setInputFiles(path.join(ROOT, "e2e", "fixtures", "quiz.bank.yaml"));
      await clickHighlighted(page, page.locator("#uploadYamlBtn"), "上传 YAML");
      await page.waitForTimeout(1800);
      await showStep(page, "加载题库", "从题库列表加载“题库测试小测”为当前小测。");
      await clickHighlighted(page, page.locator('#quizBankList button[onclick*="quiz.bank"]').filter({ hasText: "加载" }), "加载题库");
      await clickHighlighted(page, page.locator("#confirmOkBtn"), "确认加载");
      await page.waitForTimeout(1800);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "当前题库已切换", "答题页显示当前加载的小测标题。", 2400);
      await uploadQuizYAML(seed.courseId, seed.teacherCookie);
    });

    await record(browser, "08_题库_编辑器与分享", "02_题库编辑器预览.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await showStep(page, "题库编辑器", "打开题库编辑器，查看题目结构和预览效果。");
      await page.goto(`${BASE_URL}/quiz-editor?course_id=${seed.courseId}&quiz_id=quiz.sample`);
      await page.waitForLoadState("domcontentloaded");
      await page.waitForTimeout(1400);
      const preview = page.getByText("预览").first();
      await clickHighlighted(page, preview, "切换预览");
      await page.waitForTimeout(1600);
      await showStep(page, "预览简答题", "编辑器预览会显示文本、图片、代码和组合题型。", 2600);
    });

    await createStudentSubmissionFast(seed, "SHARE1");
    await record(browser, "08_题库_编辑器与分享", "03_分享小测与公开结果页.mp4", async (page) => {
      const share = await createShare(seed);
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "分享小测", "教师可以创建公开分享链接，让评委免登录查看结果。");
      await clickHighlighted(page, page.locator("#shareQuizBtn"), "打开分享弹窗");
      await page.locator("#shareModal").waitFor({ state: "visible" });
      await page.waitForTimeout(2200);
      await showStep(page, "公开结果页", "使用分享链接打开免登录结果页。");
      await page.goto(`${BASE_URL}/share?token=${share.share_token}`);
      await page.locator("#content").waitFor({ state: "visible", timeout: 8000 });
      await showStep(page, "分享页已加载", "公开页展示课程、小测标题和答题记录，不需要教师登录。", 2800);
    });

    await createStudentSubmissionFast(seed, "DETAIL1");
    await record(browser, "09_成绩导出总结与MCP", "01_答题详情与CSV导出.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-attempts");
      await showStep(page, "查看答题详情", "点击学生记录的查看按钮，打开本次答题明细。");
      const row = page.locator("#attemptsList tr").filter({ hasText: "演示学生DETAIL1" });
      await clickHighlighted(page, row.getByText("查看").first(), "查看详情");
      await page.locator("#detailModal").waitFor({ state: "visible" });
      await page.waitForTimeout(2600);
      await clickHighlighted(page, page.locator("#detailModal").getByText("关闭"), "关闭详情");
      await showStep(page, "导出 CSV", "教师可导出答题记录，用于赛后分析或存档。");
      await highlightTarget(page, page.locator("#exportCsvBtn"), "导出 CSV");
      await page.waitForTimeout(2400);
    });

    await createStudentSubmissionFast(seed, "SUM1");
    await record(browser, "09_成绩导出总结与MCP", "02_当前题库总结与历史趋势.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-summary");
      await page.waitForTimeout(2200);
      await showStep(page, "当前题库统计", "这里展示答题人数、得分和题目统计，并提供 AI 总结入口。", 2600);
      await highlightTarget(page, page.locator("#genSummaryBtn"), "生成当前题库总结");
      await page.waitForTimeout(1700);
      await highlightTarget(page, page.locator("#genHistoryBtn"), "生成历史趋势总结");
      await page.waitForTimeout(2400);
    });

    await record(browser, "09_成绩导出总结与MCP", "03_MCP长效Token配置.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-summary");
      await page.locator("#mcpEnabledToggle").scrollIntoViewIfNeeded();
      await showStep(page, "MCP 智能助手", "开启长效 MCP Token 后，可在 Claude Desktop / Cursor 查询课程数据。");
      await clickHighlighted(page, page.locator("#mcpEnabledToggle"), "启用 MCP Token");
      await page.waitForTimeout(2600);
      await highlightTarget(page, page.locator("#mcpConfig"), "可复制配置");
      await page.waitForTimeout(2600);
    });

    const gradeAssignmentId = `demo_grade_${Date.now()}`;
    await publishHomeworkAssignmentFast(seed, gradeAssignmentId);
    await createHomeworkSubmissionFast(seed, gradeAssignmentId, "评分演示一", "GRADE001", "grade-one");
    await createHomeworkSubmissionFast(seed, gradeAssignmentId, "评分演示二", "GRADE002", "grade-two");
    await record(browser, "10_作业评分与Q&A", "01_作业评分页_匿名_自动保存.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-homework"]'), "作业管理");
      await selectHighlighted(page, page.locator("#homeworkAssignmentFilter"), gradeAssignmentId, "选择作业");
      await clickHighlighted(page, page.locator("#refreshHomeworkBtn"), "刷新提交");
      const row = page.locator("#homeworkSubmissionsList tr").filter({ hasText: "GRADE002" });
      await highlightTarget(page, row.getByRole("button", { name: "评分", exact: true }), "评分入口");
      const submissionId = await page.evaluate(
        async ({ courseId, assignmentId }) => {
          const r = await fetch(`/api/teacher/courses/homework/submissions?course_id=${courseId}&assignment_id=${encodeURIComponent(assignmentId)}`, { credentials: "include" });
          const data = await r.json();
          const item = (data.items || []).find((it) => it.student_no === "GRADE002");
          return item && item.id;
        },
        { courseId: seed.courseId, assignmentId: gradeAssignmentId },
      );
      if (!submissionId) throw new Error("GRADE002 submission not found");
      await page.goto(`${BASE_URL}/teacher/homework-grade?course_id=${seed.courseId}&assignment_id=${encodeURIComponent(gradeAssignmentId)}&submission_id=${encodeURIComponent(submissionId)}`);
      await page.waitForLoadState("domcontentloaded");
      await page.locator("#scoreInput").waitFor({ state: "visible" });
      await showStep(page, "作业评分页", "左侧预览 PDF，右侧填写分数和评语。");
      await clickHighlighted(page, page.locator("#anonymousToggle"), "匿名评分");
      await fillHighlighted(page, page.locator("#scoreInput"), "88.5", "填写分数");
      await fillHighlighted(page, page.locator("#feedbackInput"), "结构完整，继续加强分析。", "填写评语");
      await page.waitForTimeout(2400);
    });

    await record(browser, "10_作业评分与Q&A", "02_成绩公布与学生查看.mp4", async (page) => {
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-homework"]'), "作业管理");
      await selectHighlighted(page, page.locator("#homeworkAssignmentFilter"), gradeAssignmentId, "选择已评分作业");
      await showStep(page, "公布成绩", "教师控制学生是否能看到分数和评语。");
      await clickHighlighted(page, page.locator("#homeworkGradeVisibilityBtn"), "公布成绩");
      await clickHighlighted(page, page.locator("#confirmOkBtn"), "确认公布");
      await page.waitForTimeout(1600);
      await showStep(page, "学生查看成绩", "切换到学生端，刷新作业页即可看到已公布评分。");
      await page.goto(`${BASE_URL}/`);
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await switchStudentTab(page, "tab-homework");
      await selectHighlighted(page, page.locator("#assignment_id"), gradeAssignmentId, "选择作业");
      await fillHighlighted(page, page.locator("#hw_name"), "评分演示一", "姓名");
      await fillHighlighted(page, page.locator("#hw_student_no"), "GRADE001", "学号");
      await fillHighlighted(page, page.locator("#hw_class_name"), "评分班", "班级");
      await fillHighlighted(page, page.locator("#hw_secret_key"), "grade-one", "密钥");
      await clickHighlighted(page, page.locator("#enterBtn"), "进入作业");
      await page.locator("#submissionCard").waitFor({ state: "visible" });
      await clickHighlighted(page, page.locator("#refreshBtn"), "刷新状态");
      await page.waitForTimeout(2600);
    });

    const qaAssignmentId = `demo_qa_${Date.now()}`;
    await publishHomeworkAssignmentFast(seed, qaAssignmentId);
    await record(browser, "10_作业评分与Q&A", "03_学生提问与教师回复.mp4", async (page) => {
      await page.goto(`${BASE_URL}/`);
      await showStep(page, "学生进入作业 Q&A", "学生先进入作业会话，再打开 Q&A 提问。");
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await switchStudentTab(page, "tab-homework");
      await selectHighlighted(page, page.locator("#assignment_id"), qaAssignmentId, "选择作业");
      await fillHighlighted(page, page.locator("#hw_name"), "QA演示学生", "姓名");
      await fillHighlighted(page, page.locator("#hw_student_no"), "QA001", "学号");
      await fillHighlighted(page, page.locator("#hw_class_name"), "QA班", "班级");
      await fillHighlighted(page, page.locator("#hw_secret_key"), "qa-secret", "密钥");
      await clickHighlighted(page, page.locator("#enterBtn"), "进入作业");
      await page.locator("#submissionCard").waitFor({ state: "visible" });
      await clickHighlighted(page, page.locator("#qaToggleBtn"), "打开 Q&A");
      await page.waitForURL("**/student/qa**", { timeout: 8000 });
      await clickHighlighted(page, page.locator("#newIssueBtn"), "新建提问");
      await fillHighlighted(page, page.locator("#newIssueMsg"), "这道题我不太理解，什么是递归？", "填写问题");
      await clickHighlighted(page, page.locator("button").filter({ hasText: "提交" }).last(), "提交问题");
      await page.waitForTimeout(2000);
      await showStep(page, "教师回复", "切换到教师 Q&A 管理页，查看并回复学生问题。");
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-homework"]'), "作业管理");
      await clickHighlighted(page, page.locator("#homeworkQABtn"), "进入 Q&A 管理");
      await page.waitForURL("**/teacher/qa**", { timeout: 8000 });
      await page.locator("#pageTitle").waitFor({ state: "visible" });
      await clickHighlighted(page, page.locator("#issueList").getByText("什么是递归").first(), "查看学生问题");
      await fillHighlighted(page, page.locator("#replyMsg"), "递归就是函数在解决问题时调用自己，通常要有明确的终止条件。", "填写教师回复");
      await clickHighlighted(page, page.locator("#replyForm button").filter({ hasText: "回复" }), "回复学生");
      await page.waitForTimeout(2600);
    });

    const secretAssignmentId = `demo_secret_${Date.now()}`;
    await publishHomeworkAssignmentFast(seed, secretAssignmentId);
    await createHomeworkSubmissionFast(seed, secretAssignmentId, "密钥学生", "SECRET001", "correct-secret");
    await record(browser, "10_作业评分与Q&A", "04_作业密钥错误与可见性控制.mp4", async (page) => {
      await page.goto(`${BASE_URL}/`);
      await showStep(page, "密钥校验", "同一学生作业会话由学号和密钥保护。这里演示输错密钥会被拒绝。");
      await fillHighlighted(page, page.locator("#codeInput"), seed.inviteCode, "输入邀请码");
      await clickHighlighted(page, page.locator("#codeBtn"), "进入课程");
      await page.locator("#panel").waitFor({ state: "visible" });
      await switchStudentTab(page, "tab-homework");
      await selectHighlighted(page, page.locator("#assignment_id"), secretAssignmentId, "选择作业");
      await fillHighlighted(page, page.locator("#hw_name"), "密钥学生", "姓名");
      await fillHighlighted(page, page.locator("#hw_student_no"), "SECRET001", "同一学号");
      await fillHighlighted(page, page.locator("#hw_class_name"), "密钥班", "班级");
      await fillHighlighted(page, page.locator("#hw_secret_key"), "wrong-secret", "错误密钥");
      await clickHighlighted(page, page.locator("#enterBtn"), "尝试进入");
      await page.waitForTimeout(2200);
      await showStep(page, "教师可见性控制", "教师可以隐藏或恢复作业，控制学生是否可见。");
      await loginTeacher(page);
      await selectCourse(page, seed.courseId);
      await switchTeacherTab(page, "tab-upload");
      await clickHighlighted(page, page.locator('.sub-tab-btn[data-subtab="sub-homework"]'), "作业管理");
      await highlightTarget(page, page.locator("#homeworkAssignmentsList").filter({ hasText: secretAssignmentId }), "作业可见性入口");
      await page.waitForTimeout(2600);
    });

    await record(browser, "11_管理员_安全运维", "01_管理员错误登录与教师权限隔离.mp4", async (page) => {
      await page.goto(`${BASE_URL}/admin`);
      await showStep(page, "管理员错误登录", "错误密码会停留在登录页，不能进入系统管理。");
      await fillHighlighted(page, page.locator("#loginId"), ADMIN_ID, "管理员账号");
      await fillHighlighted(page, page.locator("#loginPwd"), "wrong-password", "错误密码");
      await clickHighlighted(page, page.locator("#loginBtn"), "尝试登录");
      await page.waitForTimeout(2000);
      await showStep(page, "教师权限隔离", "普通教师登录后访问 /admin，会被限制在非管理员权限之外。");
      await loginTeacher(page);
      await page.goto(`${BASE_URL}/admin`);
      await page.waitForTimeout(2600);
    });

    await record(browser, "11_管理员_安全运维", "02_AI配置保存重新加载.mp4", async (page) => {
      await loginAdmin(page);
      await switchAdminTab(page, "ai");
      await showStep(page, "AI 配置", "管理员可维护 AI endpoint、模型和密钥。演示使用临时配置写入隔离测试库。");
      await fillHighlighted(page, page.locator("#aiEndpoint"), "https://example.invalid/v1", "Endpoint");
      await fillHighlighted(page, page.locator("#aiModel"), "demo-model", "模型名");
      await fillHighlighted(page, page.locator("#aiKey"), "demo-key", "API Key");
      await clickHighlighted(page, page.locator("#aiSaveBtn"), "保存配置");
      await page.waitForTimeout(1600);
      await clickHighlighted(page, page.locator("#aiLoadBtn"), "重新加载");
      await page.waitForTimeout(2200);
    });

    await record(browser, "11_管理员_安全运维", "03_系统快照生成与下载入口.mp4", async (page) => {
      await loginAdmin(page);
      await switchAdminTab(page, "snapshots");
      await showStep(page, "系统快照", "快照用于升级前备份、下载和恢复。录制环境使用临时快照目录。");
      await clickHighlighted(page, page.locator("#snapshotCreateLiteBtn"), "生成轻量快照");
      await page.waitForTimeout(2600);
      await highlightTarget(page, page.locator("#snapshotTable"), "快照列表");
      await page.waitForTimeout(2600);
      await highlightTarget(page, page.locator("#snapshotUploadRestoreBtn"), "上传恢复入口");
      await page.waitForTimeout(1800);
    });

    await record(browser, "11_管理员_安全运维", "04_系统更新安全提示.mp4", async (page) => {
      await loginAdmin(page);
      await switchAdminTab(page, "update");
      await showStep(page, "系统更新", "管理员可先查看升级窗口判断，再决定是否检查更新。");
      await highlightTarget(page, page.locator("#updateSafetyHint"), "升级窗口提示");
      await page.waitForTimeout(2200);
      await highlightTarget(page, page.locator("#updateCheckBtn"), "检查更新入口");
      await page.waitForTimeout(2200);
      await highlightTarget(page, page.locator("#updatePullBtn"), "拉取编译按钮");
      await page.waitForTimeout(1800);
      await showStep(page, "不执行破坏性动作", "演示只展示更新入口和安全提示，不拉取代码、不重启服务。", 2600);
    });
  } finally {
    await browser.close();
    if (serverProcess && !serverProcess.killed) {
      serverProcess.kill("SIGTERM");
    }
  }

  console.log(`[demo] Videos written to ${VIDEOS_DIR}`);
}

main().catch((error) => {
  if (serverProcess && !serverProcess.killed) serverProcess.kill("SIGTERM");
  console.error(error);
  process.exit(1);
});
