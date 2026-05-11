// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { defineConfig, devices } from "@playwright/test";

const PORT = 19876;
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "html" : "list",
  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
    {
      name: "firefox-smoke",
      testMatch: /responsive-smoke\.spec\.ts/,
      use: { browserName: "firefox" },
    },
    {
      name: "webkit-smoke",
      testMatch: /responsive-smoke\.spec\.ts/,
      use: { browserName: "webkit" },
    },
    {
      name: "mobile-chrome-smoke",
      testMatch: /responsive-smoke\.spec\.ts/,
      use: { ...devices["Pixel 7"] },
    },
    {
      name: "mobile-safari-smoke",
      testMatch: /responsive-smoke\.spec\.ts/,
      use: { ...devices["iPhone 14"] },
    },
    {
      name: "ipad-safari-smoke",
      testMatch: /responsive-smoke\.spec\.ts/,
      use: { ...devices["iPad Pro 11"] },
    },
  ],
});

export { PORT, BASE_URL };
