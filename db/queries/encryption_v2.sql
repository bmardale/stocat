-- name: GetEncryptionDeployment :one
SELECT public_id FROM encryption_deployment WHERE singleton = true;
