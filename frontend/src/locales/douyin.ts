const messages: Record<string, string> = {
  '抖音 App 分享交接': 'Douyin App sharing handoff',
  '仅已获批 H5 应用可用。用户在抖音内选择账号并最终确认；交接记录只表示请求打开 App，不证明 App 已打开或内容已发布。': 'Available only to approved H5 applications. Choose your account and confirm in Douyin. A handoff record means an App launch was requested; it does not prove that the App opened or published content.',
  '抖音应用审批、公开 HTTPS 媒体入口或短期 ticket 未就绪，当前仅可导出素材。': 'Douyin approval, a public HTTPS media endpoint or an ephemeral ticket is not ready. Only file export is currently available.',
  '分享链接最多有效5分钟，持有链接者可读取这份素材；撤销只阻止后续下载，无法收回已下载副本。图片最大20MiB，视频最大128MiB。': 'Sharing links last up to five minutes and allow anyone holding the link to read this media. Revocation blocks future downloads but cannot recall existing copies. Images: up to 20 MiB; videos: up to 128 MiB.',
  '抖音交接素材': 'Douyin handoff media',
  '抖音分享标题（可选）': 'Douyin share title (optional)',
  '我拥有素材及相关人物、音乐的分享权利。': 'I have sharing rights for the media, depicted people and music.',
  '我同意为此素材创建短期公开读取链接，并自行在抖音确认目标账号和发布。': 'I consent to a temporary public read link for this media and will confirm the account and publication in Douyin myself.',
  '准备抖音短期分享链接': 'Prepare a temporary Douyin sharing link',
  '打开抖音并在 App 内确认': 'Open Douyin and confirm in the App',
  '链接到期时间：': 'Link expires: ',
  '交接链接已过期，请重新确认素材后创建新链接。': 'The handoff link expired. Confirm the media again to create a new link.',
  '分享交接地址无效，未打开外部应用。': 'The sharing handoff address is invalid. No external App was opened.',
  '交接操作未确认，请刷新记录或使用原请求核对。': 'The handoff operation is unconfirmed. Refresh the records or check using the original request.',
  '使用原请求核对交接结果': 'Check handoff using the original request',
  '撤销此抖音素材链接': 'Revoke this Douyin media link',
  'explicit media sharing and rights consent required': 'Explicit media sharing and rights consent is required.',
  'Douyin image maximum 20MiB; video maximum 128MiB': 'Douyin images allow up to 20 MiB; videos allow up to 128 MiB.',
  'unsupported Douyin media': 'This media type is not supported for Douyin sharing.',
  'explicit handoff confirmation required': 'Explicit handoff confirmation is required.',
  'invalid handoff state': 'This handoff state does not allow the requested action.'
}
export default messages
