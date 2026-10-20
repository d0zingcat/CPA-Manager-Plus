import { Navigate, type RouteObject } from 'react-router-dom';
import { AccessLayout } from '@/features/readonly/AccessLayout';
import { MainLayout } from '@/components/layout/MainLayout';
import { DemoPage } from '@/pages/DemoPage';
import { LoginPage } from '@/pages/LoginPage';
import { ProtectedRoute } from '@/router/ProtectedRoute';
import { RootShell } from './RootShell';

const appRoutes: RouteObject[] = [
  {
    element: <RootShell />,
    children: __DEMO_SITE__
      ? [
          { index: true, element: <Navigate to="/demo" replace /> },
          { path: '/demo/*', element: <DemoPage /> },
          { path: '*', element: <Navigate to="/demo" replace /> },
        ]
      : [
          { path: '/login', element: <LoginPage /> },
          {
            path: '/*',
            element: (
              <ProtectedRoute>
                <AccessLayout admin={<MainLayout />} />
              </ProtectedRoute>
            ),
          },
        ],
  },
];

export const createAppRoutes = (): RouteObject[] => appRoutes;
