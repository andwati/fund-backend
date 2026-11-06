-- +goose Up
-- Baseline of the schema as of Drizzle migration 0011 (the Next.js app's final migration).
-- Existing databases already contain these objects: do NOT run this against them.
-- Use `fund-migrate baseline` to record it as applied instead. Fresh databases (tests, dev) run it normally.

CREATE TABLE public.account (
    id text NOT NULL,
    account_id text NOT NULL,
    provider_id text NOT NULL,
    user_id text NOT NULL,
    access_token text,
    refresh_token text,
    id_token text,
    access_token_expires_at timestamp with time zone,
    refresh_token_expires_at timestamp with time zone,
    scope text,
    password text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.allocation (
    id text NOT NULL,
    contribution_id text NOT NULL,
    item_id text NOT NULL,
    amount integer NOT NULL
);

CREATE TABLE public.contribution (
    id text NOT NULL,
    fund_id text NOT NULL,
    date date NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    kind text DEFAULT 'deposit'::text NOT NULL,
    CONSTRAINT contribution_kind_check CHECK ((kind = ANY (ARRAY['deposit'::text, 'withdrawal'::text])))
);

CREATE TABLE public.fund (
    id text NOT NULL,
    user_id text NOT NULL,
    name text NOT NULL,
    pace integer,
    currency text DEFAULT 'KES'::text NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    timezone_confirmed boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    onboarding_completed boolean DEFAULT false NOT NULL
);

CREATE TABLE public.fund_invite (
    id text NOT NULL,
    fund_id text NOT NULL,
    email text NOT NULL,
    role text NOT NULL,
    token text NOT NULL,
    invited_by text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.fund_member (
    fund_id text NOT NULL,
    user_id text NOT NULL,
    role text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.fund_payday (
    fund_id text NOT NULL,
    day integer NOT NULL,
    CONSTRAINT fund_payday_day_check CHECK (((day >= 1) AND (day <= 31)))
);

CREATE TABLE public.fund_reminder_pref (
    fund_id text NOT NULL,
    user_id text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    last_reminder_on date
);

CREATE TABLE public.item (
    id text NOT NULL,
    fund_id text NOT NULL,
    name text NOT NULL,
    price integer NOT NULL,
    deadline date NOT NULL,
    plan_start date NOT NULL,
    "position" integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone,
    actual_price integer,
    purchased_on date
);

CREATE TABLE public.payday_amount (
    fund_id text NOT NULL,
    date date NOT NULL,
    amount integer NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.payday_skip (
    fund_id text NOT NULL,
    date date NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.session (
    id text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    token text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    ip_address text,
    user_agent text,
    user_id text NOT NULL
);

CREATE TABLE public."user" (
    id text NOT NULL,
    name text NOT NULL,
    email text NOT NULL,
    email_verified boolean DEFAULT false NOT NULL,
    image text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    joined_via_invite boolean DEFAULT false NOT NULL
);

CREATE TABLE public.verification (
    id text NOT NULL,
    identifier text NOT NULL,
    value text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.allocation
    ADD CONSTRAINT allocation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.contribution
    ADD CONSTRAINT contribution_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.fund_invite
    ADD CONSTRAINT fund_invite_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.fund_invite
    ADD CONSTRAINT fund_invite_token_unique UNIQUE (token);

ALTER TABLE ONLY public.fund_member
    ADD CONSTRAINT fund_member_fund_id_user_id_pk PRIMARY KEY (fund_id, user_id);

ALTER TABLE ONLY public.fund_payday
    ADD CONSTRAINT fund_payday_fund_id_day_pk PRIMARY KEY (fund_id, day);

ALTER TABLE ONLY public.fund
    ADD CONSTRAINT fund_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.fund_reminder_pref
    ADD CONSTRAINT fund_reminder_pref_fund_id_user_id_pk PRIMARY KEY (fund_id, user_id);

ALTER TABLE ONLY public.item
    ADD CONSTRAINT item_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payday_amount
    ADD CONSTRAINT payday_amount_fund_id_date_pk PRIMARY KEY (fund_id, date);

ALTER TABLE ONLY public.payday_skip
    ADD CONSTRAINT payday_skip_fund_id_date_pk PRIMARY KEY (fund_id, date);

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_token_unique UNIQUE (token);

ALTER TABLE ONLY public."user"
    ADD CONSTRAINT user_email_unique UNIQUE (email);

ALTER TABLE ONLY public."user"
    ADD CONSTRAINT user_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.verification
    ADD CONSTRAINT verification_pkey PRIMARY KEY (id);

CREATE INDEX allocation_contribution_id_idx ON public.allocation USING btree (contribution_id);

CREATE INDEX allocation_item_id_idx ON public.allocation USING btree (item_id);

CREATE INDEX contribution_fund_id_idx ON public.contribution USING btree (fund_id);

CREATE INDEX fund_invite_fund_id_idx ON public.fund_invite USING btree (fund_id);

CREATE INDEX fund_member_user_id_idx ON public.fund_member USING btree (user_id);

CREATE INDEX fund_reminder_pref_user_id_idx ON public.fund_reminder_pref USING btree (user_id);

CREATE INDEX fund_user_id_idx ON public.fund USING btree (user_id);

CREATE INDEX item_fund_id_idx ON public.item USING btree (fund_id);

CREATE UNIQUE INDEX user_email_lower_idx ON public."user" USING btree (lower(email));

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.allocation
    ADD CONSTRAINT allocation_contribution_id_contribution_id_fk FOREIGN KEY (contribution_id) REFERENCES public.contribution(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.allocation
    ADD CONSTRAINT allocation_item_id_item_id_fk FOREIGN KEY (item_id) REFERENCES public.item(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.contribution
    ADD CONSTRAINT contribution_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_invite
    ADD CONSTRAINT fund_invite_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_invite
    ADD CONSTRAINT fund_invite_invited_by_user_id_fk FOREIGN KEY (invited_by) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_member
    ADD CONSTRAINT fund_member_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_member
    ADD CONSTRAINT fund_member_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_payday
    ADD CONSTRAINT fund_payday_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_reminder_pref
    ADD CONSTRAINT fund_reminder_pref_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund_reminder_pref
    ADD CONSTRAINT fund_reminder_pref_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.fund
    ADD CONSTRAINT fund_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.item
    ADD CONSTRAINT item_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payday_amount
    ADD CONSTRAINT payday_amount_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payday_skip
    ADD CONSTRAINT payday_skip_fund_id_fund_id_fk FOREIGN KEY (fund_id) REFERENCES public.fund(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

-- +goose Down
-- Baseline is not reversible.
SELECT 1;
