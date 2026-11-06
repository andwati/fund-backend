// Subjects/text from the real React Email templates and the real unsubscribe token code.
import { test } from "vitest";
import { writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
// @ts-ignore
import { render } from "/home/ian/dev/fund/node_modules/react-email/dist/index.mjs";
import React from "/home/ian/dev/fund/node_modules/react/index.js";

test("gen mail", async () => {
  process.env.DATABASE_URL = "postgres://x/y";
  process.env.BETTER_AUTH_SECRET = "0123456789abcdef-secret";
  const un = await import("@/server/reminder-unsubscribe");
  const { inviteEmailSubject, inviteRoleLabel } = await import("@/server/invite-email-copy");
  const E = "/home/ian/dev/fund/emails/";
  const mods: any = {
    verify: await import(E + "verify-email"), verifyNew: await import(E + "verify-email-change"),
    changeConfirm: await import(E + "change-email-confirm"), reset: await import(E + "reset-password"),
    changed: await import(E + "password-changed"), deleted: await import(E + "account-deleted"),
    invite: await import(E + "fund-invite"), owner: await import(E + "fund-ownership-transfer"),
    reminder: await import(E + "payday-reminder"),
  };
  const h = React.createElement;
  const U = "https://app.test/verify-email?token=T123";
  const els: Record<string, any> = {
    verify: h(mods.verify.VerifyEmail, { url: U }),
    verifyNew: h(mods.verifyNew.VerifyEmailChange ?? mods.verifyNew.default, { url: U }),
    changeConfirm: h(mods.changeConfirm.ChangeEmailConfirm ?? mods.changeConfirm.default, { url: U, newEmail: "new@example.com" }),
    reset: h(mods.reset.ResetPassword ?? mods.reset.default, { url: U, email: "ann@example.com" }),
    changed: h(mods.changed.PasswordChanged ?? mods.changed.default, { email: "ann@example.com" }),
    deleted: h(mods.deleted.default, { email: "ann@example.com" }),
    inviteNew: h(mods.invite.FundInvite ?? mods.invite.default, { fundName: "Wedding", inviter: "Sam", role: "editor", url: "https://app.test/invite/TOK", hasAccount: false }),
    inviteHas: h(mods.invite.FundInvite ?? mods.invite.default, { fundName: "Wedding", inviter: "Sam", role: "viewer", url: "https://app.test/invite/TOK", hasAccount: true }),
    ownerGot: h(mods.owner.OwnershipReceived, { fundName: "Wedding", otherPerson: "Sam", url: "https://app.test/settings" }),
    ownerGave: h(mods.owner.OwnershipHandedOff, { fundName: "Wedding", otherPerson: "Alex", url: "https://app.test/" }),
    reminder: h(mods.reminder.PaydayReminder ?? mods.reminder.default, { fundName: "Home", payday: "Fri 31 Oct", amount: "KES 12,000", url: "https://app.test/contributions?new=1", unsubscribeUrl: "https://api.test/unsubscribe/reminders?token=X" }),
  };
  const texts: Record<string, string> = {};
  for (const [k, el] of Object.entries(els)) texts[k] = await render(el, { plainText: true });

  const secret = "0123456789abcdef-secret";
  const tokens = [["fund-1", "user-1"], ["a-b-c", "u:with:colons"], ["f", "日本語"]].map(([f, u]) => ({ f, u, token: un.signReminderUnsubscribe(f!, u!), ok: un.verifyReminderUnsubscribe(un.signReminderUnsubscribe(f!, u!)) }));
  const bad = ["", "nodot", ".sig", "a:b.", "x.y", un.signReminderUnsubscribe("f", "u") + "x", "f:u." + "A".repeat(43)].map((t) => ({ t, out: un.verifyReminderUnsubscribe(t) }));
  const url = un.reminderUnsubscribeUrl("https://api.test/", "fund-1", "user-1");
  writeFileSync(process.env.OUT!, gzipSync(JSON.stringify({ secret, texts, tokens, bad, url, subjects: { inviteNew: inviteEmailSubject("Wedding", false), inviteHas: inviteEmailSubject("Wedding", true), role: [inviteRoleLabel("editor"), inviteRoleLabel("viewer")] } })));
});
