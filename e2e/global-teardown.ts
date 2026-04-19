// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { stopServer, cleanupTmp } from "./helpers/server";

export default async function globalTeardown() {
  console.log("[e2e] Stopping server …");
  stopServer();
  console.log("[e2e] Cleaning up temp files …");
  cleanupTmp();
  console.log("[e2e] Teardown complete.");
}
