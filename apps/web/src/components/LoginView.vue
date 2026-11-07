<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAuthStore } from '../store/auth'

const { state, login } = useAuthStore()

interface DemoAccount {
  username: string
  password: string
  role: string
  description: string
}

const demoAccounts: DemoAccount[] = [
  { username: 'admin', password: 'Admin@123456', role: '管理员', description: '全部权限：规则、标定、任意相机' },
  { username: 'operator', password: 'Operator@123', role: '值班员', description: '处置事件/工单，仅负责区域内相机' },
  { username: 'viewer', password: 'Viewer@123', role: '观察员', description: '只读：相机/事件/轨迹/统计/工单' },
]

const username = ref('')
const password = ref('')
const formError = ref('')

const displayError = computed(() => formError.value || state.error)

function fill(account: DemoAccount): void {
  username.value = account.username
  password.value = account.password
  formError.value = ''
}

async function submit(): Promise<void> {
  formError.value = ''
  if (!username.value.trim()) {
    formError.value = '请输入用户名'
    return
  }
  if (!password.value) {
    formError.value = '请输入密码'
    return
  }
  await login(username.value, password.value)
}
</script>

<template>
  <div class="login-shell">
    <div class="login-glow" aria-hidden="true" />
    <main class="login-card">
      <header class="login-brand">
        <span class="brand-mark login-mark">DT</span>
        <div>
          <h1>数字孪生安防平台</h1>
          <p>P2.0 · 登录 / RBAC / 规则 / 工单</p>
        </div>
      </header>

      <form class="login-form" @submit.prevent="submit">
        <label class="login-field">
          <span>用户名</span>
          <input
            v-model="username"
            type="text"
            autocomplete="username"
            placeholder="admin / operator / viewer"
            :disabled="state.loggingIn"
          />
        </label>
        <label class="login-field">
          <span>密码</span>
          <input
            v-model="password"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
            :disabled="state.loggingIn"
          />
        </label>

        <p v-if="displayError" class="form-error login-error">{{ displayError }}</p>

        <button class="login-submit" type="submit" :disabled="state.loggingIn">
          <span v-if="state.loggingIn" class="spinner login-spinner" />
          {{ state.loggingIn ? '登录中…' : '登 录' }}
        </button>
      </form>

      <section class="demo-accounts">
        <header>
          <h2>演示账号</h2>
          <span>点击自动填入</span>
        </header>
        <button
          v-for="account in demoAccounts"
          :key="account.username"
          type="button"
          class="demo-account"
          :disabled="state.loggingIn"
          @click="fill(account)"
        >
          <span class="demo-account-role">{{ account.role }}</span>
          <span class="demo-account-name mono">{{ account.username }}</span>
          <span class="demo-account-password mono">{{ account.password }}</span>
          <span class="demo-account-desc">{{ account.description }}</span>
        </button>
      </section>

      <p class="login-hint">
        登录后按角色显示权限：观察员只读；值班员可处置事件/工单并修改负责区域内相机；管理员额外可配置规则与标定。
      </p>
    </main>

    <footer class="login-footer">数字孪生安防平台 · P2.0 演示环境 · 请勿用于生产</footer>
  </div>
</template>
