// Generates golden vectors from the web app's real planning code.
// Run: make parity   (needs ~/dev/fund with node_modules installed)
import { test } from "vitest";
import { writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import * as plan from "/home/ian/dev/fund/src/plan/index";
import * as ledger from "/home/ian/dev/fund/src/plan/ledger";
import * as reminders from "/home/ian/dev/fund/src/plan/reminders";

function rng(seed: number) {
  return () => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const r = rng(20261106);
const int = (a: number, b: number) => a + Math.floor(r() * (b - a + 1));
const pick = <T,>(xs: T[]): T => xs[int(0, xs.length - 1)]!;
const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);
const BASE = Date.UTC(2025, 0, 1);
const dayMs = 86_400_000;
const randDate = (lo = 0, hi = 1460) => iso(BASE + int(lo, hi) * dayMs);
const addDays = (d: string, n: number) => iso(Date.parse(d + "T00:00:00Z") + n * dayMs);
const ZONES = ["UTC", "Africa/Nairobi", "America/Los_Angeles", "Pacific/Kiritimati", "Asia/Kolkata", "Not/AZone"];

type Case = any;
const cases: Case[] = [];
// mode: "wide" random; "tight" small money so gaps land near the 0.5 threshold;
// "tomorrow" sets today to the day before a payday so reminders fire.
for (let n = 0; n < 700; n++) {
  const mode = n < 300 ? "wide" : n < 500 ? "tight" : "tomorrow";
  const dayPool = [1, 5, 15, 28, 29, 30, 31, 31, 25, 10];
  const days = Array.from({ length: int(1, 4) }, () => pick(dayPool));
  let today = randDate(200, 1100);
  const tight = mode === "tight";
  const items = Array.from({ length: int(mode === "tomorrow" ? 1 : 0, 4) }, (_, i) => {
    const planStart = r() < 0.5 ? addDays(today, -int(0, 200)) : randDate(0, 1200);
    return { id: `i${i}`, name: `Item ${i}`, price: tight ? int(1, 40) : int(1, 200) * 100, planStart, deadline: addDays(planStart, int(5, 900)) };
  });
  const contributions = Array.from({ length: int(0, 8) }, (_, i) => {
    const used = new Set<string>();
    const lines = Array.from({ length: int(1, 3) }, () => pick(items.length ? items : [{ id: "x" }]).id)
      .filter((id) => (used.has(id) ? false : (used.add(id), true)))
      .map((itemId) => ({ itemId, amount: tight ? int(1, 6) : int(1, 120) * 10 }));
    return { id: `c${i}`, date: randDate(0, 1200), note: "", kind: r() < 0.25 ? "withdrawal" : r() < 0.5 ? "deposit" : undefined, allocations: lines };
  });
  const skips = Array.from({ length: int(0, 3) }, () => randDate(300, 1200));
  const adjustments = Array.from({ length: int(0, 3) }, () => ({ date: randDate(300, 1200), amount: int(1, 80) * 50 }));
  const pace = r() < 0.5 ? null : int(0, 30) * 100;
  const timezone = pick(ZONES);
  const schedule = plan.scheduleOf({ paydays: days }, skips, adjustments);
  if (mode === "tomorrow") {
    const nxt = plan.nextPayday(today, true, schedule);
    if (nxt) today = addDays(nxt, -1);
  }
  const input = { schedule, pace, timezone, items, contributions, today } as any;
  const progress = plan.compute(input);
  cases.push({
    input: { days, skips, adjustments, pace, timezone, items, contributions, today },
    progress,
    risks: plan.deadlineRisks(input),
    savedByItem: Object.fromEntries(plan.savedByItem(contributions as any)),
    upcoming: reminders.upcomingPaydays(progress),
    reminderPayday: reminders.reminderPayday(progress),
    expectedOn: progress.schedule.map((s) => [s.date, reminders.expectedOn(progress, s.date)]),
  });
}

const ledgers = Array.from({ length: 200 }, () => {
  const lines = Array.from({ length: int(0, 10) }, (_, i) => ({
    contributionId: `c${int(0, 5)}`, itemId: `i${int(0, 2)}`, date: randDate(0, 30),
    kind: r() < 0.4 ? "withdrawal" : "deposit", amount: int(1, 50), createdAt: BASE + int(0, 5) * 1000,
  }));
  const only = r() < 0.3 ? ["i0", "i1"] : null;
  return { lines, only, breach: ledger.firstLedgerBreach(lines as any, only ? new Set(only) : undefined) };
});

const instants = ["2026-03-29T00:30:00Z", "2026-10-25T00:30:00Z", "2026-11-06T07:59:00Z", "2026-11-06T08:00:00Z", "2026-12-31T23:30:00Z", "2027-01-01T00:00:01Z", "2028-02-29T12:00:00Z"];
const times = ZONES.flatMap((tz) => instants.map((i) => ({
  tz, now: i, today: plan.todayIn(tz, new Date(i)), hour: plan.hourIn(tz, new Date(i)),
  allowed: [null, plan.todayIn(tz, new Date(i))].map((last) => reminders.reminderSendAllowed(tz, last, new Date(i))),
})));

test("generate", () => {
  writeFileSync(process.env.OUT ?? "parity.json.gz", gzipSync(JSON.stringify({ cases, ledgers, times })));
});
