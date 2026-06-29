// Upload helpers for both job-create modes.
//  - Multipart direct: single file in the job-create request itself.
//  - Presigned: init -> PUT each part -> complete, yielding an uploadId.
// Multi-file tools (merge, image-to-pdf) always use presigned, because k6's
// object-multipart cannot repeat the `files` field; presigned uploadIds[] is
// the clean multi-file path and is exactly what the SPA does for big files.
import http from 'k6/http';
import { postJSON, envelope, authHeaders, url } from './http.js';
import { uploadBytes } from './metrics.js';

function etagOf(res) {
  return res.headers['Etag'] || res.headers['ETag'] || res.headers['etag'] || '';
}

// Presigned upload of one fixture {data, filename, contentType}. Returns uploadId.
export function presignedUpload(file, token) {
  const init = postJSON('/api/upload/init', {
    fileName: file.filename,
    fileSize: file.data.byteLength,
    contentType: file.contentType,
  }, token);
  if (init.status !== 200 && init.status !== 201) {
    return { ok: false, status: init.status, body: init.body };
  }
  const d = envelope(init).data || {};
  const uploadId = d.uploadId;
  const partSize = d.partSize || file.data.byteLength;
  const parts = d.parts || [];
  const done = [];
  for (const p of parts) {
    const start = (p.partNumber - 1) * partSize;
    const end = Math.min(start + partSize, file.data.byteLength);
    const chunk = file.data.slice(start, end);
    const put = http.put(p.url, chunk, {
      headers: { 'Content-Type': 'application/octet-stream' },
      tags: { kind: 'storage' },
    });
    if (put.status !== 200) return { ok: false, status: put.status, body: put.body, stage: 'put' };
    uploadBytes.add(end - start);
    done.push({ partNumber: p.partNumber, etag: etagOf(put) });
  }
  const complete = postJSON(`/api/upload/${uploadId}/complete`, { parts: done }, token);
  if (complete.status !== 200 && complete.status !== 201) {
    return { ok: false, status: complete.status, body: complete.body, stage: 'complete' };
  }
  return { ok: true, uploadId };
}

// Build the multipart body for a single-file direct job create.
export function multipartBody(file, options) {
  return {
    files: http.file(file.data, file.filename, file.contentType),
    options: JSON.stringify(options || {}),
  };
}

export { authHeaders, url };
