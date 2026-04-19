// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { execSync, spawn, ChildProcess } from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as net from "net";

const ROOT = path.resolve(__dirname, "../..");
const BIN = path.join(ROOT, "bin", "server");
const MIGRATE_BIN = path.join(ROOT, "bin", "migrate");
const TMP = path.join(ROOT, "e2e", ".tmp");

export const PORT = 19876;
export const BASE_URL = `http://127.0.0.1:${PORT}`;

export const DATA_DIR = path.join(TMP, "data");
export const METADATA_DIR = path.join(TMP, "metadata");
export const QUIZ_ASSETS_DIR = path.join(TMP, "quiz_assets");
const DB_PATH = path.join(DATA_DIR, "app.db");

const ADMIN_ID = "e2e_admin";
const ADMIN_NAME = "E2E管理员";
const ADMIN_PASSWORD = "e2e-admin-pwd";

export { ADMIN_ID, ADMIN_NAME, ADMIN_PASSWORD };

let serverProcess: ChildProcess | null = null;

export function buildServer(): void {
  execSync("make build-server build-migrate", { cwd: ROOT, stdio: "inherit" });
}

export function prepareTmpDirs(): void {
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(DATA_DIR, { recursive: true });
  fs.mkdirSync(METADATA_DIR, { recursive: true });
  fs.mkdirSync(QUIZ_ASSETS_DIR, { recursive: true });
}

export function bootstrapAdmin(): void {
  execSync(
    [
      MIGRATE_BIN,
      "upgrade",
      `--db=${DB_PATH}`,
      `--metadata-dir=${METADATA_DIR}`,
      `--teacher-id=${ADMIN_ID}`,
      `--teacher-name=${ADMIN_NAME}`,
      `--password=${ADMIN_PASSWORD}`,
    ].join(" "),
    { cwd: ROOT, stdio: "inherit" }
  );
}

export function startServer(): ChildProcess {
  const proc = spawn(BIN, [], {
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
  proc.stdout?.on("data", (d: Buffer) => process.stdout.write(d));
  proc.stderr?.on("data", (d: Buffer) => process.stderr.write(d));
  serverProcess = proc;
  return proc;
}

export async function waitForReady(timeoutMs = 15_000): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const ok = await checkPort(PORT);
      if (ok) return;
    } catch {
      // ignore
    }
    await sleep(200);
  }
  throw new Error(`Server not ready after ${timeoutMs}ms`);
}

function checkPort(port: number): Promise<boolean> {
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

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

export function stopServer(): void {
  if (serverProcess && !serverProcess.killed) {
    serverProcess.kill("SIGTERM");
    serverProcess = null;
  }
}

export function cleanupTmp(): void {
  fs.rmSync(TMP, { recursive: true, force: true });
}
