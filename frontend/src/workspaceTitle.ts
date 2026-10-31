export function workspaceTitle(path: string) {
  const titles: Record<string, string> = {'/settings/preferences': '系统设置', '/publish': '发布中心', '/studio/workspaces': '图像作品', '/digital-humans/video': '视频型数字人', '/digital-humans/interactive': '交互型数字人', '/digital-humans/3d': '3D 数字人', '/library': '创作资产库', '/studio': '图像创作', '/assets': '素材库', '/projects': '短剧项目', '/settings/organization': '组织与成员', '/settings/models': '模型管理', '/settings/billing': '模型使用统计'}
  if (titles[path]) return titles[path]
  if (path.startsWith('/publish/')) return '发布中心'
  if (path.startsWith('/projects/')) return ({brief: '项目概览', script: '剧本', storyboard: '分镜', media: '素材', review: '审片与导出', production: '制作工作台'} as Record<string,string>)[path.split('/').at(-1) ?? ''] ?? '短剧工作台'
  return '工作空间'
}
