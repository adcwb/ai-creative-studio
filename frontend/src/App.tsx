import { Outlet, RouterProvider, createBrowserRouter, Navigate } from 'react-router-dom'
import { AuthGate } from './components/AuthGate'
import { AppLayout } from './layouts/AppLayout'
import { ImageWorkspace } from './features/images/ImageWorkspace'
import { NotFoundPage } from './pages/NotFoundPage'
import { RouteErrorPage } from './pages/RouteErrorPage'
import './drama.css'
import './layouts/app.css'

function SessionGate() {
  return <AuthGate>{(session, logout) => <Outlet context={{session, logout}} />}</AuthGate>
}

const router = createBrowserRouter([
  {element: <SessionGate />, children: [
    {path: '/social-callback', lazy: async () => ({Component: (await import('./components/SocialTikTokPanel')).SocialOAuthCallback})},
    {element: <AppLayout />, children: [
      {index: true, element: <Navigate to="/studio" replace />},
      {element: <ImageWorkspace />, errorElement: <RouteErrorPage />, children: [
        {path: 'studio', lazy: async () => ({Component: (await import('./pages/ImagePage')).ImagePage})},
        {path: 'assets', lazy: async () => ({Component: (await import('./pages/AssetsPage')).AssetsPage})},
        {path: 'projects', lazy: async () => ({Component: (await import('./pages/ProjectsPage')).ProjectsPage})},
        {path: 'projects/:projectId/:section', lazy: async () => ({Component: (await import('./pages/ProjectPage')).ProjectPage})},
        {path: 'settings/:section', lazy: async () => ({Component: (await import('./pages/SettingsPage')).SettingsPage})},
        {path: '*', element: <NotFoundPage />},
      ]},
    ]},
  ]},
])

export default function App() {
  return <RouterProvider router={router} />
}
