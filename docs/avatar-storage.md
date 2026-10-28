# Cloudinary avatar storage

The backend processes avatar images locally and uploads only the sanitized 512x512
PNG derivative to Cloudinary. The mobile app still sends multipart field file to
POST /api/profile/me/avatar with its TrackIQ bearer token. No frontend API change
or unsigned Cloudinary upload preset is needed.

## Setup

1. Create a Cloudinary account and open its product environment.
2. Open Settings > API Keys. Copy the cloud name, API key and API secret directly
   into the server environment. Keep the API secret on the server; do not put it
   in Flutter, Git, screenshots or chat.
3. Set these three variables in /var/www/trackfund/.env:

    CLOUDINARY_CLOUD_NAME=your-cloud-name
    CLOUDINARY_API_KEY='your-api-key'
    CLOUDINARY_API_SECRET='your-api-secret'

4. Remove the old AVATAR_S3_* and AVATAR_PUBLIC_BASE_URL entries. They are no
   longer used. No bucket, custom image-domain DNS or AWS signing setup is needed.
5. Rebuild the API, source the updated environment and restart PM2 with --update-env.
   For nginx allow multipart overhead using client_max_body_size 6m; run nginx -t
   before reloading nginx.
6. With a fresh TrackIQ access token, upload a file:

    curl -i https://thetrackiq.co/api/profile/me/avatar \
      -H "Authorization: Bearer $ACCESS_TOKEN" \
      -F 'file=@avatar.png;type=image/png'

7. Open the returned HTTPS photoUrl, then GET /api/profile/me. The same URL must be
   returned. Upload again and check it changes to a new URL. These are public
   profile images; anyone who has the image URL can read the image.

Official API-key setup: https://cloudinary.com/documentation/cloudinary_get_started
Signed API calls: https://cloudinary.com/documentation/authentication_signatures
Upload/delete API: https://cloudinary.com/documentation/image_upload_api_reference

## Processing and replacement

Maximum 5 MiB encoded; at most 12 megapixels and 8192 pixels on either axis. Two
concurrent decoders per API process bound memory use. Actual JPEG, PNG or WebP
must match the supplied MIME type. Decode, square center-crop and re-encode remove
original metadata, including EXIF/GPS. Original bytes are never uploaded. Animated
WebP may be rejected; rotation metadata is stripped rather than applied.

Uploads use random public IDs under profile/<owner-hash>/<random-id> with overwrite
false. Public IDs omit the .png extension; delivery URLs include it. URLs use
https://res.cloudinary.com/<cloud-name>/image/upload/v1/profile/...png and do not
expire. This default delivery URL needs no separate CDN configuration.

PostgreSQL reserves the key before upload; the profile pointer and activation
commit atomically. Earlier managed objects are queued only after replacement.
Cleanup runs every ten seconds, keeps a five-minute grace period and retries
failures hourly. Abandoned uncommitted uploads expire after one hour. Cloudinary
destroy uses invalidate=true to request CDN invalidation; propagation is not
instant. Already absent images are treated as successfully removed.

All three variables may be blank: uploads then return 503
avatar_storage_not_configured. Partial/invalid configuration fails startup. No new
database migration is needed for this provider switch. Any pre-existing S3 images
and URLs are not automatically migrated: keep their old hosting available until
replaced and arrange removal from the old provider separately. This switch assumes
the newly added avatar feature has not yet been populated on S3.

Local tests verify signed uploads, response validation and deletion using a fake
Cloudinary transport. An upload against your real product environment remains a
post-deployment check and requires your credentials.
