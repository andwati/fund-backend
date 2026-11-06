// Golden vectors for money/format and export, from the web app's real code.
import { test } from "vitest";
import { writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import * as fmt from "/home/ian/dev/fund/src/lib/format";
import * as ex from "/home/ian/dev/fund/src/lib/export";

let seed = 777;
const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };
const int = (a: number, b: number) => a + Math.floor(rnd() * (b - a + 1));
const pick = <T,>(xs: T[]): T => xs[int(0, xs.length - 1)]!;

const NASTY = ["Laptop", "=SUM(A1)", "+1", "-5 items", "@home", "a,b", 'say "hi"', "line\nbreak", "tab\there", "\tlead", "\rcr", "café ☕", "<b>&amp;</b>", "'quoted", "", "  spaced  ", "日本語", "🙂 emoji"];
const codes = [...Intl.supportedValuesOf("currency"), "kes", " usd ", "XXX", "Points", "KSH", "abc", "ABCD", ""];
const amounts = [0, 1, 999, 1000, 1234.5, 1234.4999, 0.5, 1.5, 2.5, 1e9, 123456789, -1, -1234, -0.4, -0.5, 12.345];

const money = codes.flatMap((c) => amounts.map((a) => ({ c, a, out: fmt.money(c, a) })));
const long = ["2026-10-20", "2024-02-29", "2026-01-01", "2026-12-31", "1999-07-04"].flatMap((d) => [false, true].map((y) => ({ d, y, out: fmt.fmtLong(d, y) })));

const itemRows = Array.from({ length: 40 }, () => ({
  name: pick(NASTY), price: int(1, 99999), deadline: "2027-03-01", planStart: "2026-10-20", saved: int(0, 5000),
  actualPrice: rnd() < 0.5 ? null : int(1, 9999), purchasedOn: rnd() < 0.5 ? null : "2026-11-01", archived: rnd() < 0.5,
}));
const items = ex.itemsCsv(itemRows);

const names = new Map<string, string>();
const contribRows = Array.from({ length: 40 }, () => ({
  date: "2026-10-" + String(int(10, 28)), note: pick(NASTY), kind: pick(["deposit", "withdrawal"]) as "deposit" | "withdrawal",
  allocations: Array.from({ length: int(0, 3) }, (_, i) => { const id = `i${int(0, 4)}`; names.set(id, pick(NASTY)); return { itemId: id, amount: int(1, 5000) }; }),
}));
const contribs = ex.contributionsCsv(contribRows, (id) => names.get(id) ?? "Item");

const csvTexts = [
  contribs, items, "", "﻿date,note,item,amount\r\n2026-10-20,hi,Laptop,100\r\n",
  "date,note,item,amount\n2026-10-20,\"a,b\",Laptop,100\n2026-10-20,\"a,b\",Phone,50\n",
  "# fund-contributions v2\ndate,kind,note,item,amount\n2026-10-20,Deposit,x,Laptop,10\n2026-10-20,withdrawal,x,Laptop,5\n",
  "# fund-contributions v3\ndate,kind,note,item,amount\n",
  "# fund-contributions v2\nwrong,header\n",
  "date,note,item,amount\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,1e3\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,0x10\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,010\n",
  "date,note,item,amount\n2026-10-20,n,Laptop, 12 \n",
  "date,note,item,amount\n2026-10-20,n,Laptop,12.5\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,-3\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,0\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,Infinity\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,0b11\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,1_000\n",
  "date,note,item,amount\n2026-10-20,n,Laptop,.5e1\n",
  "date,note,item,amount\n2026-1-20,n,Laptop,5\n",
  "date,note,item,amount\n2026-10-20,n,,5\n",
  "# fund-contributions v2\ndate,kind,note,item,amount\n2026-10-20,gift,x,Laptop,10\n",
  "date,note,item,amount\n2026-10-20,'=SUM(A1),Laptop,5\n2026-10-20,'plain,Laptop,5\n",
  "date,note,item,amount\n" + Array.from({ length: 2001 }, () => "2026-10-20,n,Laptop,1").join("\n"),
  "date,note,item,amount\r\n2026-10-20,\"multi\r\nline\",Laptop,5\r\n\r\n,,,\r\n",
];
const reads = csvTexts.map((text) => ({ text, out: ex.readContributionsCsv(text) }));
const parses = csvTexts.slice(0, 12).map((text) => ({ text, out: ex.parseCsv(text) }));

const ics = [
  { fundName: "My, Fund; \\ x\nY", paydays: ["2026-10-31", "2026-12-31", "2028-02-29"], stamp: "2026-11-06T09:08:07.123Z" },
  { fundName: "Empty", paydays: [], stamp: "2026-01-01T00:00:00.000Z" },
].map((i) => ({ ...i, out: ex.scheduleIcs({ fundName: i.fundName, paydays: i.paydays, summary: (d) => `Payday: ${d}, set aside; "x"\nnext`, stamp: new Date(i.stamp) }) }));

const folders = [["My Fund", "abcdef123456"], ["  --Hello, World!--  ", "x"], ["日本語", "12345678"], ["", "abc"], ["a/b\\c", "123456789"]].map(([n, id]) => ({ n, id, out: ex.fundExportFolder(n!, id!) }));

const bundle: ex.AccountExportBundle = {
  exportedAt: "2026-11-06T10:00:00.000Z",
  account: { id: "u1", name: "Ann <ann> & co", email: "ann@example.com", emailVerified: true, createdAt: "2026-01-01T00:00:00.000Z" },
  memberships: [{ fundId: "f9", name: "Shared", role: "editor", paydays: [15, 31], pace: null, currency: "KES", timezone: "Africa/Nairobi", timezoneConfirmed: true, remindersEnabled: false, joinedAt: "2026-02-02T00:00:00.000Z" }],
  ownedFunds: [{
    folder: ex.fundExportFolder("Home Fund", "f1234567890"),
    fund: { id: "f1234567890", name: "Home Fund", paydays: [31], pace: 2500, currency: "USD", timezone: "UTC", timezoneConfirmed: false, remindersEnabled: true, createdAt: "2026-03-03T00:00:00.000Z" },
    members: [{ userId: "u2", name: "Bob", role: "viewer" }], pendingInvites: [{ id: "inv1", role: "editor", expiresAt: "2026-11-13T00:00:00.000Z" }],
    items: [{ id: "i1", name: "=Laptop", price: 1000, deadline: "2027-01-01", planStart: "2026-10-01", position: 0, saved: 200, actualPrice: null, purchasedOn: null, archived: false },
            { id: "i2", name: "Phone, \"new\"", price: 500, deadline: "2026-12-01", planStart: "2026-10-01", position: 1, saved: 500, actualPrice: 480, purchasedOn: "2026-11-01", archived: true }],
    contributions: [{ id: "c1", date: "2026-10-31", note: "pay", kind: "deposit", allocations: [{ id: "a1", itemId: "i1", amount: 200 }, { id: "a2", itemId: "ghost", amount: 5 }] },
                    { id: "c2", date: "2026-11-02", note: "", kind: "withdrawal", allocations: [] }],
  }, { folder: "owned-empty", fund: { id: "e", name: "Empty", paydays: [1], pace: null, currency: "KES", timezone: "UTC", timezoneConfirmed: true, remindersEnabled: false, createdAt: "2026-04-04T00:00:00.000Z" }, members: [], pendingInvites: [], items: [], contributions: [] }],
};
const account = { bundle, files: ex.accountExportFiles(bundle) };

test("gen lib", () => {
  writeFileSync(process.env.OUT!, gzipSync(JSON.stringify({ money, long, itemRows, items, contribRows, itemNames: Object.fromEntries(names), contribs, reads, parses, ics, folders, account })));
});
