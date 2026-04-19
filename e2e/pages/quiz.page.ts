// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

import { type Page, type Locator } from "@playwright/test";

export class QuizPage {
  readonly page: Page;
  readonly title: Locator;
  readonly exitBtn: Locator;
  readonly questions: Locator;
  readonly submitBtn: Locator;

  constructor(page: Page) {
    this.page = page;
    this.title = page.locator("#title");
    this.exitBtn = page.locator("#exitBtn");
    this.questions = page.locator("#questions");
    this.submitBtn = page.locator("#submit");
  }

  async waitForLoad() {
    await this.questions.locator(".card").first().waitFor({
      state: "visible",
      timeout: 10_000,
    });
  }

  async getTitle(): Promise<string> {
    return (await this.title.textContent()) ?? "";
  }

  /**
   * Answer all questions by sending answers via the API directly.
   * Questions are shuffled per attempt, so UI-based index clicking
   * is unreliable. Instead we use fetch() to POST answers and then
   * trigger the submit flow from the UI.
   */
  async answerAllViaAPI(
    answers: Record<string, string>
  ): Promise<void> {
    for (const [questionId, answer] of Object.entries(answers)) {
      await this.page.evaluate(
        async ({ qid, ans }) => {
          const r = await fetch("/api/answer", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ question_id: qid, answer: ans }),
          });
          if (!r.ok) throw new Error(`answer failed: ${r.status}`);
        },
        { qid: questionId, ans: answer }
      );
    }
  }

  async submit() {
    await this.submitBtn.click();
    await this.page.waitForURL("**/result", { timeout: 10_000 });
  }
}
