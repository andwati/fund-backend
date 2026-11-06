// Go -> Node: Go seeded the database. The web app's REAL loadFund() must return exactly what Go
// returned; then the web app mutates the data (its own server functions) and records its payloads
// so Go can read the changes (Go -> Node -> Go).
import { test, vi, expect } from "vitest";
import { readFileSync, writeFileSync } from "node:fs";

const session = vi.hoisted(() => ({ id: "user-a", email: "user-a@example.com", fund: undefined as string | undefined }));
vi.mock("@/server/mail", () => ({ sendReminderEmail: async () => {}, sendInviteEmail: async () => {}, sendOwnershipTransferEmails: async () => {} }));
vi.mock("next/headers", () => ({ headers: async () => new Headers() }));
vi.mock("@/server/auth", () => ({ auth: { api: { signInEmail: async () => {} } } }));
vi.mock("@/server/http", () => {
  class HttpError extends Error { constructor(public status: number, message: string) { super(message); } }
  return {
    HttpError,
    requireUser: async () => ({ id: session.id, name: session.id, email: session.email }),
    preferredFundId: async () => session.fund,
    rememberFund: async (id: string) => { session.fund = id; },
    clientIp: async () => "127.0.0.1",
    zodMessage: (e: { issues: { message: string }[] }) => e.issues[0]?.message ?? "invalid",
  };
});

const day = 86_400_000;
const iso = (offset: number) => new Date(Date.now() + offset * day).toISOString().slice(0, 10);

test("node reads Go's data, then mutates it", async () => {
  process.env.DATABASE_URL = process.env.PARITY_DATABASE_URL!;
  process.env.BETTER_AUTH_SECRET = "0123456789abcdef-secret";
  const api = await import("@/server/fund");
  const { setReminderEnabled } = await import("@/server/reminder-pref");
  const { loadFund } = await import("@/features/fund/data");
  const plan = await import("@/plan");

  const golden = JSON.parse(readFileSync(process.env.GO_PAYLOADS!, "utf8"));
  const as = (id: string) => { session.id = id; session.email = `${id}@example.com`; session.fund = golden.fundId; };
  const norm = (v: unknown) => JSON.parse(JSON.stringify(v));

  for (const uid of ["user-a", "user-b"]) {
    as(uid);
    expect(norm(await loadFund()), `payload for ${uid}`).toEqual(golden.users[uid]);
  }

  // The web app now changes Go-written data with its own code.
  as("user-a");
  const { items, fund, skips } = await api.getFund();
  const laptop = items.find((i) => i.name === "Laptop")!.id;
  await api.createContribution({ date: iso(-1), note: "from node", allocations: [{ itemId: laptop, amount: 10 }] });
  await api.updateItem(laptop, { name: "Laptop Pro", price: 1200 });
  await api.restorePayday(skips[0]!);
  const today = plan.todayIn(fund.timezone);
  const next = plan.nextPayday(today, true, plan.scheduleOf(fund))!;
  await api.adjustPayday({ date: next, amount: 4000 });
  await api.createItem({ name: "Added by node", price: 90, deadline: iso(40) });
  await setReminderEnabled(false);

  const after: Record<string, unknown> = {};
  for (const uid of ["user-a", "user-b"]) { as(uid); after[uid] = norm(await loadFund()); }
  writeFileSync(process.env.OUT_AFTER!, JSON.stringify({ fundId: golden.fundId, users: after }, null, 1));
});
