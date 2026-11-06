// Seeds a rich scenario through the web app's REAL server functions into a database, then dumps
// what the web app's own loadFund() returns for two users. The Go test reads the same database and
// must produce an identical payload (Node -> Go direction).
import { test, vi } from "vitest";
import { writeFileSync } from "node:fs";

const session = vi.hoisted(() => ({ id: "user-a", email: "user-a@example.com", fund: undefined as string | undefined, invites: [] as { to: string; url: string }[] }));

vi.mock("@/server/mail", () => ({
  sendReminderEmail: async () => {},
  sendInviteEmail: async (to: string, d: { url: string }) => { session.invites.push({ to, url: d.url }); },
  sendOwnershipTransferEmails: async () => {},
}));
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

test("seed and dump", async () => {
  process.env.DATABASE_URL = process.env.PARITY_DATABASE_URL!;
  process.env.BETTER_AUTH_SECRET = "0123456789abcdef-secret";
  const { db, user } = await import("@/db");
  const api = await import("@/server/fund");
  const funds = await import("@/server/funds");
  const { setReminderEnabled } = await import("@/server/reminder-pref");
  const { loadFund } = await import("@/features/fund/data");
  const plan = await import("@/plan");

  await db.delete(user);
  for (const id of ["user-a", "user-b"]) await db.insert(user).values({ id, name: id, email: `${id}@example.com`, emailVerified: true });
  const as = (id: string) => { session.id = id; session.email = `${id}@example.com`; };
  as("user-a");

  const first = await api.getFund();
  const fundId = first.fund.id;
  await api.updateFund({ name: "  Home  ", paydays: [25, 10], pace: 2500, currency: "USD", timezone: "Africa/Nairobi", timezoneConfirmed: true });
  for (const [name, price, off] of [["Laptop", 1000, 200], ["Phone", 500, 100], ["Bike", 300, 60], ["Café ☕", 80, 30]] as const) {
    await api.createItem({ name, price, deadline: iso(off) });
  }
  const items = (await api.getFund()).items;
  const id = (n: string) => items.find((i) => i.name === n)!.id;

  await api.createContribution({ date: iso(-20), note: "start", allocations: [{ itemId: id("Laptop"), amount: 400 }, { itemId: id("Phone"), amount: 100 }] });
  await api.createContribution({ id: "4f0c2f2e-2a52-4a53-9a4e-0d6a3b1c7e11", date: iso(-10), note: " bonus ", allocations: [{ itemId: id("Laptop"), amount: 200 }] });
  await api.createContribution({ date: iso(-5), note: "", kind: "withdrawal", allocations: [{ itemId: id("Laptop"), amount: 100 }] });
  await api.createContribution({ date: iso(-5), note: "tie", allocations: [{ itemId: id("Bike"), amount: 50 }] });
  await api.importContributions([{ date: iso(-3), note: "imported", line: 3, kind: "deposit", allocations: [{ item: "phone", amount: 25 }, { item: "Café ☕", amount: 5 }] }]);
  await api.setItemArchived(id("Bike"), true, { actualPrice: 280, purchasedOn: iso(-2) });
  await api.moveItem(id("Phone"), "up");

  const fund = (await api.getFund()).fund;
  const today = plan.todayIn(fund.timezone);
  const sch = plan.scheduleOf(fund);
  const p1 = plan.nextPayday(today, true, sch)!;
  const p2 = plan.nextPayday(p1, false, sch)!;
  const p3 = plan.nextPayday(p2, false, sch)!;
  await api.skipPayday(p1);
  await api.adjustPayday({ date: p2, amount: 3000 });
  await api.adjustPayday({ date: p3, amount: 1200 });
  await setReminderEnabled(true);

  await funds.inviteMember({ email: "user-b@example.com", role: "viewer" });
  await funds.inviteMember({ email: "pending@example.com", role: "editor" });
  const token = session.invites.find((i) => i.to === "user-b@example.com")!.url.split("/").pop()!;
  as("user-b");
  await funds.acceptInvite(token);
  await setReminderEnabled(false);
  session.fund = fundId;
  const asB = JSON.parse(JSON.stringify(await loadFund()));
  as("user-a");
  session.fund = fundId;
  const asA = JSON.parse(JSON.stringify(await loadFund()));
  writeFileSync(process.env.OUT!, JSON.stringify({ fundId, users: { "user-a": asA, "user-b": asB } }, null, 1));
});
