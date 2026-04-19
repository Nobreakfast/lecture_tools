// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import {
  buildServer,
  prepareTmpDirs,
  bootstrapAdmin,
  startServer,
  waitForReady,
} from "./helpers/server";
import { seedData } from "./helpers/seed";

export default async function globalSetup() {
  console.log("[e2e] Building server …");
  buildServer();

  console.log("[e2e] Preparing temp directories …");
  prepareTmpDirs();

  console.log("[e2e] Bootstrapping admin user via migrate …");
  bootstrapAdmin();

  console.log("[e2e] Starting server …");
  startServer();
  await waitForReady();
  console.log("[e2e] Server ready.");

  console.log("[e2e] Seeding test data …");
  await seedData();
  console.log("[e2e] Setup complete.");
}
