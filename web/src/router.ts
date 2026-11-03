import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/share/:token', component: () => import('./views/ShareView.vue'), meta: { public: true } },
    { path: '/mobile-tools', component: () => import('./views/MobileToolsView.vue'), meta: { nativeTools: true } },
    { path: '/change-password', component: () => import('./views/ChangePasswordView.vue') },
    { path: '/workspace', component: () => import('./views/WorkspaceView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/workspace' },
  ],
})

router.beforeEach(async (to) => {
  // The native loopback gateway supplies authenticated tool requests and context.
  if (to.meta.nativeTools) return
  const auth = useAuthStore()
  if (!auth.checked) await auth.check()
  if (!to.meta.public && !auth.user) return '/login'
  if (auth.user?.forcePasswordChange && to.path !== '/change-password') return '/change-password'
  if (!auth.user?.forcePasswordChange && to.path === '/change-password') return '/workspace'
  if (to.path === '/login' && auth.user) return '/workspace'
})

export default router
