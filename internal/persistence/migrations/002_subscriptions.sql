CREATE TABLE subscriptions (
  user_id uuid PRIMARY KEY REFERENCES users,
  plan_code text NOT NULL,
  status text NOT NULL CHECK (status IN ('active','past_due','cancelled','trial')),
  documents_used integer NOT NULL DEFAULT 0,
  current_period_ends_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON subscriptions (status, current_period_ends_at);
