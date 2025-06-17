CREATE TABLE IF NOT EXISTS accounts
(
    id                       INTEGER PRIMARY KEY,
    name                     TEXT      NOT NULL,
    display_name             TEXT      NOT NULL,
    email                    TEXT      NOT NULL,

    imap_server              TEXT      NOT NULL,
    imap_port                INTEGER   NOT NULL DEFAULT 993,
    imap_username            TEXT      NOT NULL,
    imap_use_ssl             BOOLEAN   NOT NULL DEFAULT TRUE,
    imap_auth_method         TEXT      NOT NULL DEFAULT 'plain',

    smtp_server              TEXT      NOT NULL,
    smtp_port                INTEGER   NOT NULL DEFAULT 587,
    smtp_username            TEXT      NOT NULL,
    smtp_use_tls             BOOLEAN   NOT NULL DEFAULT TRUE,
    smtp_auth_method         TEXT      NOT NULL DEFAULT 'plain',

    refresh_interval_minutes INTEGER   NOT NULL DEFAULT 15,
    signature                TEXT,
    is_default               BOOLEAN   NOT NULL DEFAULT FALSE,
    created_at               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS folders
(
    id         INTEGER PRIMARY KEY,
    account_id INTEGER NOT NULL,
    name       TEXT    NOT NULL,

    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS threads
(
    id                  INTEGER PRIMARY KEY,
    account_id          INTEGER   NOT NULL,
    subject             TEXT      NOT NULL,
    snippet             TEXT,
    is_read             BOOLEAN   NOT NULL DEFAULT FALSE,
    is_starred          BOOLEAN   NOT NULL DEFAULT FALSE,
    has_attachments     BOOLEAN   NOT NULL DEFAULT FALSE,
    message_count       INTEGER   NOT NULL DEFAULT 1,
    latest_message_date TIMESTAMP NOT NULL,

    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS emails
(
    id            INTEGER PRIMARY KEY,
    uid           INTEGER   NOT NULL,
    thread_id     INTEGER   NOT NULL,
    account_id    INTEGER   NOT NULL,
    folder_id     INTEGER   NOT NULL,
    message_id    TEXT      NOT NULL,
    from_address  TEXT      NOT NULL,
    from_name     TEXT,
    to_addresses  TEXT      NOT NULL,
    cc_addresses  TEXT,
    bcc_addresses TEXT,
    reference_id  TEXT,
    subject       TEXT      NOT NULL,
    body_text     TEXT,
    body_html     TEXT,
    received_date TIMESTAMP NOT NULL,
    is_read       BOOLEAN   NOT NULL DEFAULT FALSE,
    is_starred    BOOLEAN   NOT NULL DEFAULT FALSE,
    is_draft      BOOLEAN   NOT NULL DEFAULT FALSE,

    FOREIGN KEY (thread_id) REFERENCES threads (id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id) REFERENCES folders (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS attachments
(
    id         INTEGER PRIMARY KEY,
    email_id   INTEGER NOT NULL,
    filename   TEXT    NOT NULL,
    mime_type  TEXT    NOT NULL,
    size_bytes INTEGER NOT NULL,
    content    BLOB,
    local_path TEXT,

    FOREIGN KEY (email_id) REFERENCES emails (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_threads_account_id ON threads (account_id);
CREATE INDEX IF NOT EXISTS idx_emails_thread_id ON emails (thread_id);
CREATE INDEX IF NOT EXISTS idx_emails_account_id ON emails (account_id);
CREATE INDEX IF NOT EXISTS idx_emails_folder_id ON emails (folder_id);
CREATE INDEX IF NOT EXISTS idx_attachments_email_id ON attachments (email_id);
