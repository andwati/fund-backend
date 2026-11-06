// Password vectors from better-auth's own crypto, plus a check that hashes made by Go verify in Node.
import { test, expect } from "vitest";
import { writeFileSync, existsSync, readFileSync } from "node:fs";
// @ts-ignore - resolved from the web app's install
import { hashPassword, verifyPassword } from "/home/ian/dev/fund/node_modules/better-auth/dist/crypto/index.mjs";

const PASSWORDS = ["correct horse battery staple", "p@ss", "12345678", "ünïcödé-pässwörd", "éclair-12345", "ﬁnal-ﬂag-12345", "日本語のパスワード", "a".repeat(128), "with space and \"quotes\" 123", "🙂emoji🙂pass"];

test("gen auth vectors", async () => {
  const vectors = [] as { password: string; hash: string }[];
  for (const password of PASSWORDS) {
    vectors.push({ password, hash: await hashPassword(password) });
    vectors.push({ password, hash: await hashPassword(password) }); // different salt
  }
  writeFileSync(process.env.OUT!, JSON.stringify(vectors));
});

test("node verifies hashes produced by Go", async () => {
  const file = process.env.GO_HASHES;
  if (!file || !existsSync(file)) return; // only when Go has written them
  const rows = JSON.parse(readFileSync(file, "utf8")) as { password: string; hash: string }[];
  expect(rows.length).toBeGreaterThan(0);
  for (const { password, hash } of rows) {
    expect(await verifyPassword({ hash, password })).toBe(true);
    expect(await verifyPassword({ hash, password: password + "x" })).toBe(false);
  }
});
