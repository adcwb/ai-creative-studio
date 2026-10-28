# Social platform contracts checked for iteration 10

Checked against official documentation on 2026-10-28. These are protocol and eligibility references, **not real account integration results**. Runtime coverage is narrower than the platform's total API surface; see [implementation and limitations](iteration-10-social.md).

| Platform | Official capability | Implemented scope / boundary |
| --- | --- | --- |
| TikTok | Creator information, direct video posting, inbox video upload, photos, status queries | This iteration's execution path is video. Application approval/audit and OAuth are required; privacy must be selected by the user. Uploading a draft is not publication. |
| Douyin | Approved H5/SDK handoff for creative apps; separate enterprise/bound-account server posting | H5 handoff uses an expiring server ticket and controlled media URL. Actual App behavior and permissions remain untested. Server posting is not implemented. |
| Xiaohongshu | Approved App sharing; current access intake paused | Export only in this iteration. No invented server-side note publishing or final-publication status. |

## TikTok sources

- [Sharing guidelines](https://developers.tiktok.com/docs/en/content-sharing-guidelines/)
- [Video Direct Post](https://developers.tiktok.com/docs/en/content-posting-api-reference-direct-post/)
- [Photo posting](https://developers.tiktok.com/docs/en/content-posting-api-reference-photo-post/)
- [Status fetch](https://developers.tiktok.com/docs/en/content-posting-api-reference-get-video-status/)
- [Media transfer](https://developers.tiktok.com/docs/en/content-posting-api-media-transfer-guide/)

The API host is `open.tiktokapis.com`. Creator information and posting/status APIs use distinct endpoints and scopes. This implementation transfers existing controlled MP4 bytes with FILE_UPLOAD; it does not assume localhost is a remotely downloadable URL. Photo APIs require a different payload and are not claimed as implemented. Only a confirmed terminal platform result may produce `published`; unknown creation results do not authorize resubmission.

## Douyin sources

[H5 sharing](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/sdk/web-app/h5/share-to-h5) specifies server-side ticket signing and the `snssdk1128://openplatform/share` scheme. Current clients require `h5.share`, `aweme.share` and ticket access. The detailed version table supersedes the older single-image overview: multi-image support exists on newer clients, but this implementation deliberately accepts one image or one video. The publishing-page shortcut is video-only. A handoff acknowledgement records the user's launch action, not a post ID or proof that the App opened.

[Video creation](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/video-management/douyin/create-video/video-create) uses `video.create.bind` and enterprise/bound-account restrictions. [Image/text creation](https://partner.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/video-management/douyin/create-image-text/create-image-text) has separate image-count and per-image limits. Neither server publishing path is implemented here.

[Share-result documentation](https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/video-management/douyin/search-video/video-share-result) contains a POST table/GET example conflict. Result lookup remains disabled rather than guessing a working contract.

## Xiaohongshu sources

[Sharing platform](https://agora.xiaohongshu.com/doc) currently reports paused access. [JS sharing](https://agora.xiaohongshu.com/doc/js) hands controlled media to the App and no longer permits prefilled title/body/topics. [Account scopes](https://openaccount.xiaohongshu.com/docs/scope) do not establish a currently open write-notes API. Login does not confer posting rights. Existing approved SDK users would still require a separate signed runtime implementation and device verification; this iteration provides authenticated downloads only.

## Shared safety model

Local tenant/project/account/source/intent identity, version checks and durable claims are not substitutes for an upstream idempotency contract. No generic upstream idempotency key is assumed. After an ambiguous create response, retain `unknown` and query only if a verified remote identifier exists. OAuth state is session-bound and one-use; tokens are encrypted server-side and excluded from public responses. No real OAuth, credential configuration, media upload or external post was performed during development.
