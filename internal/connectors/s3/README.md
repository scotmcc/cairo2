# internal/connectors/s3

**Status:** 🔲 SLOT

S3-compatible object storage connector (AWS S3, MinIO, Ceph).

Use cases:
- Document source for the `learn/` indexing pipeline (index a private document bucket)
- Blob storage for task artifacts and exported sessions
- Air-gapped deployment: use MinIO on-premise instead of AWS

Config key: `s3_endpoint`, `s3_bucket`, `s3_access_key` in `store/config/`.
