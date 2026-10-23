import { translate as t, useI18n } from "../i18n"
import { useEffect, useState } from 'react'
import { errorMessage, request, type Space } from '../api'
interface Member {id: string; email: string; role: string}

export function OrganizationPanel({space, onChange, userID}: {space?: Space; onChange: () => void; userID?: string}) {
  useI18n()
  const roleLabels: Record<string, string> = {owner: t("所有者"), admin: t("管理员"), editor: t("编辑者"), viewer: t("只读成员")}

  const [members, setMembers] = useState<Member[]>([])
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [memberID, setMemberID] = useState('')
  const [role, setRole] = useState('editor')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const canManage = space?.role === 'owner' || space?.role === 'admin'
  useEffect(() => {
    if (!space) return
    const controller = new AbortController()
    void request<Member[]>(`/organizations/${encodeURIComponent(space.id)}/members`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) setMembers(value)}).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t("成员读取失败")))})
    return () => controller.abort()
  }, [space, revision])
  async function mutate(path: string, method: string, data: unknown) {
    if (busy) return
    setBusy(true); setError(''); setNotice('')
    try {await request(path, {method, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(data)}); setRevision(value => value + 1); onChange(); setNotice(t("组织变更已保存。")); setName(''); setEmail(''); setMemberID('')} catch (reason) {setError(errorMessage(reason, t("组织变更失败")))} finally {setBusy(false)}
  }
  return <details className="panel organization-panel"><summary>{t("组织与成员 ·")}{space?.name ?? t("创建第一个组织")}{space?.role ? ` · ${roleLabels[space.role]}` : ''}</summary>{userID && <p className="drama-help">{t("我的账号 ID：")}<code style={{overflowWrap: 'anywhere'}}>{userID}</code></p>}{error && <p role="alert" className="error">{error}</p>}{notice && <p role="status" className="notice">{notice}</p>}{space?.role === 'viewer' && <p>{t("当前账户为只读成员。服务端拒绝创作与修改操作。")}</p>}<fieldset disabled={busy}><form onSubmit={event => {event.preventDefault(); void mutate('/organizations', 'POST', {name: name.trim()})}}><label>{t("新组织名称")}<input value={name} maxLength={80} required onChange={event => setName(event.target.value)} /></label><button disabled={!name.trim()} type="submit">{t("创建组织")}</button></form>{space && <><h3>{t("当前组织成员")}</h3><ul>{members.map(member => <li key={member.id}>{member.email} · {roleLabels[member.role] ?? member.role}</li>)}</ul>{canManage && <form onSubmit={event => {event.preventDefault(); void mutate(`/organizations/${encodeURIComponent(space.id)}/members`, 'PUT', {email: email.trim(), role, user_id: memberID.trim() || members.find(member => member.email.toLowerCase() === email.trim().toLowerCase())?.id})}}><p className="drama-help">{t("通过已注册邮箱添加成员或调整权限；移除后该账户不再访问此组织。所有者不会被此表单替换。")}</p><p className="drama-help">{t("添加SSO成员时请让对方分享账号ID；不凭可能被复用的邮箱自动授权。")}</p><div className="storyboard-fields"><label>{t("成员邮箱")}<input type="email" required value={email} onChange={event => setEmail(event.target.value)} /></label><label>{t("成员账号 ID（SSO必填）")}<input value={memberID} maxLength={64} onChange={event => setMemberID(event.target.value)} /></label><label>{t("成员权限")}<select value={role} onChange={event => setRole(event.target.value)}>{space.role === 'owner' && <option value="admin">{t("管理员")}</option>}<option value="editor">{t("编辑者")}</option><option value="viewer">{t("只读成员")}</option><option value="remove">{t("移除成员")}</option></select></label></div><button type="submit">{t("保存成员权限")}</button></form>}</>}</fieldset></details>
}
