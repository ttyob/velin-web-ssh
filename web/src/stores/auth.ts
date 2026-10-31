import { defineStore } from 'pinia'
import { ApiError, api, json } from '../api'
import type { User } from '../types'

export const useAuthStore = defineStore('auth', {
  state: () => ({ user: null as User | null, checked: false }),
  actions: {
    async check() { try { this.user = await api<User>('/api/auth/me') } catch { this.user = null } finally { this.checked = true } },
    async login(username: string, password: string, remember: boolean, totpCode = '', captchaID = '', captcha = '') {
      await api('/api/auth/login', { method: 'POST', body: json({ username, password, remember, totpCode, captchaID, captcha }) })
      try {
        // The login response alone does not prove the browser saved its cookie.
        // Verify it before entering a protected route such as initial setup.
        this.user = await api<User>('/api/auth/me')
        this.checked = true
      } catch (error) {
        this.user = null
        if (error instanceof ApiError && error.status === 401) {
          throw new Error('无法保存登录会话，请允许本站使用 Cookie 后重试')
        }
        throw error
      }
    },
    async logout() { try { await api('/api/auth/logout', { method: 'POST' }) } finally { this.user = null } },
  },
})
