-- name: GetAccount :one
SELECT *
FROM accounts
WHERE id = ? LIMIT 1;

-- name: ListAccounts :many
SELECT *
FROM accounts
ORDER BY name;

-- name: CreateAccount :one
INSERT INTO accounts (name, display_name, email,
                      imap_server, imap_port, imap_username, imap_use_ssl, imap_auth_method,
                      smtp_server, smtp_port, smtp_username, smtp_use_tls, smtp_auth_method,
                      refresh_interval_minutes, signature, is_default)
VALUES (?, ?, ?,
        ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?,
        ?, ?, ?) RETURNING *;

-- name: UpdateAccount :one
UPDATE accounts
SET name                     = ?,
    display_name             = ?,
    email                    = ?,
    imap_server              = ?,
    imap_port                = ?,
    imap_username            = ?,
    imap_use_ssl             = ?,
    imap_auth_method         = ?,
    smtp_server              = ?,
    smtp_port                = ?,
    smtp_username            = ?,
    smtp_use_tls             = ?,
    smtp_auth_method         = ?,
    refresh_interval_minutes = ?,
    signature                = ?,
    is_default               = ?,
    updated_at               = CURRENT_TIMESTAMP
WHERE id = ? RETURNING *;

-- name: DeleteAccount :exec
DELETE
FROM accounts
WHERE id = ?;

-- name: GetDefaultAccount :one
SELECT *
FROM accounts
WHERE is_default = TRUE LIMIT 1;

-- name: GetFolder :one
SELECT *
FROM folders
WHERE id = ? LIMIT 1;

-- name: GetFolderByName :one
SELECT id, account_id, name
FROM folders
WHERE name = ? AND account_id = ?;

-- name: ListFolders :many
SELECT *
FROM folders
WHERE account_id = ?
ORDER BY name;

-- name: CreateFolder :one
INSERT INTO folders (account_id, name)
VALUES (?, ?) RETURNING *;

-- name: UpdateFolder :one
UPDATE folders
SET name = ?
WHERE id = ? RETURNING *;

-- name: DeleteFolder :exec
DELETE
FROM folders
WHERE id = ?;

-- name: GetThread :one
SELECT *
FROM threads
WHERE id = ? LIMIT 1;

-- name: GetHighestUIDInFolder :one
SELECT uid
FROM emails
WHERE folder_id = ?
ORDER BY uid DESC LIMIT 1;

-- name: GetThreadsInFolder :many
SELECT t.*,
       folder_emails.folder_count,
       folder_emails.folder_unread_count,
       folder_emails.latest_folder_sender,
       folder_emails.latest_folder_sender_name
FROM threads t
         INNER JOIN (SELECT e.thread_id,
                            COUNT(*)                                         as folder_count,
                            SUM(CASE WHEN e.is_read = false THEN 1 ELSE 0 END) as folder_unread_count,
                            CAST((SELECT e2.from_address
                                  FROM emails e2
                                  WHERE e2.thread_id = e.thread_id
                                  ORDER BY e2.received_date DESC
                                LIMIT 1) AS TEXT) as latest_folder_sender,
                            CAST((SELECT e2.from_name
                                  FROM emails e2
                                  WHERE e2.thread_id = e.thread_id
                                  ORDER BY e2.received_date DESC
                                LIMIT 1) AS TEXT) as latest_folder_sender_name
                     FROM emails e
                     WHERE e.folder_id = ?
                     GROUP BY e.thread_id
) folder_emails ON t.id = folder_emails.thread_id
WHERE t.account_id = ?
ORDER BY t.latest_message_date DESC
    LIMIT ?;

-- name: CreateThread :one
INSERT INTO threads (account_id, subject, snippet,
                     is_read, is_starred, has_attachments,
                     message_count, latest_message_date)
VALUES (?, ?, ?,
        ?, ?, ?,
        ?, ?) RETURNING *;

-- name: UpdateThread :one
UPDATE threads
SET subject             = ?,
    snippet             = ?,
    is_read             = ?,
    is_starred          = ?,
    has_attachments     = ?,
    message_count       = ?,
    latest_message_date = ?
WHERE id = ? RETURNING *;

-- name: DeleteThread :exec
DELETE
FROM threads
WHERE id = ?;

-- name: MarkThreadRead :exec
UPDATE threads
SET is_read = TRUE
WHERE id = ?;

-- name: ToggleThreadStarred :one
UPDATE threads
SET is_starred = NOT is_starred
WHERE id = ? RETURNING is_starred;

-- name: GetEmail :one
SELECT *
FROM emails
WHERE id = ? LIMIT 1;

-- name: GetEmailByFolderAndUID :one
SELECT *
FROM emails
WHERE uid = ? AND folder_id = ?
LIMIT 1;

-- name: GetEmailByMessageID :one
SELECT thread_id,
       message_id
FROM emails
WHERE message_id = ?;

-- name: ListEmailsByThread :many
SELECT *
FROM emails
WHERE thread_id = ?
ORDER BY received_date DESC;

-- name: GetEmailsWithoutBodies :many
SELECT id, uid, folder_id, subject, from_address, received_date
FROM emails
WHERE account_id = ?
  AND folder_id = ?
  AND (body_text IS NULL OR body_text = '')
ORDER BY received_date DESC LIMIT ?;

-- name: CreateEmail :one
INSERT INTO emails (uid, thread_id, account_id, folder_id, message_id,
                    from_address, from_name, to_addresses,
                    cc_addresses, bcc_addresses, subject,
                    body_text, body_html, received_date,
                    is_read, is_starred, is_draft)
VALUES (?, ?, ?, ?, ?,
        ?, ?, ?,
        ?, ?, ?,
        ?, ?, ?,
        ?, ?, ?) RETURNING *;

-- name: UpdateEmail :one
UPDATE emails
SET folder_id  = ?,
    is_read    = ?,
    is_starred = ?,
    is_draft   = ?,
    body_text  = ?
WHERE id = ? RETURNING *;

-- name: UpdateEmailBodyAndReferences :one
UPDATE emails
SET body_text  = ? AND reference_id = ?
WHERE id = ? RETURNING *;

-- name: DeleteEmail :exec
DELETE
FROM emails
WHERE id = ?;

-- name: MarkEmailRead :exec
UPDATE emails
SET is_read = TRUE
WHERE id = ?;

-- name: ToggleEmailStarred :one
UPDATE emails
SET is_starred = NOT is_starred
WHERE id = ? RETURNING is_starred;

-- name: GetAttachment :one
SELECT *
FROM attachments
WHERE id = ? LIMIT 1;

-- name: ListAttachmentsByEmail :many
SELECT *
FROM attachments
WHERE email_id = ?;

-- name: CreateAttachment :one
INSERT INTO attachments (email_id, filename, mime_type,
                         size_bytes, content, local_path)
VALUES (?, ?, ?,
        ?, ?, ?) RETURNING *;

-- name: UpdateAttachment :one
UPDATE attachments
SET local_path = ?
WHERE id = ? RETURNING *;

-- name: DeleteAttachment :exec
DELETE
FROM attachments
WHERE id = ?;

-- name: SearchEmails :many
SELECT e.*
FROM emails e
         JOIN threads t ON e.thread_id = t.id
WHERE (e.subject LIKE '%' || ? || '%'
    OR e.body_text LIKE '%' || ? || '%'
    OR e.from_address LIKE '%' || ? || '%'
    OR e.from_name LIKE '%' || ? || '%')
  AND e.account_id = ?
ORDER BY e.received_date DESC LIMIT ?
OFFSET ?;

-- name: MoveEmail :exec
UPDATE emails
SET folder_id = ?
WHERE id = ?;

-- name: GetEmailsStats :one
SELECT COUNT(*)                                         as total_emails,
       SUM(CASE WHEN is_read = FALSE THEN 1 ELSE 0 END) as unread_count
FROM emails
WHERE account_id = ?
  AND folder_id = ?;

-- name: UpdateThreadMessageCount :exec
UPDATE threads
SET message_count = (SELECT COUNT(*)
                     FROM emails
                     WHERE thread_id = threads.id)
WHERE threads.id = ?;
