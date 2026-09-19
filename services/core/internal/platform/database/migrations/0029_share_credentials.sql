ALTER TABLE shares ADD COLUMN password_secret_ref TEXT REFERENCES secret_values(id) ON DELETE SET NULL;

ALTER TABLE share_links ADD COLUMN token_secret_ref TEXT REFERENCES secret_values(id) ON DELETE SET NULL;
