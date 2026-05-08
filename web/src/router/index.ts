import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'Dashboard',
      component: () => import('@/views/Dashboard.vue'),
    },
    {
      path: '/inputs',
      name: 'Inputs',
      component: () => import('@/views/Inputs.vue'),
    },
    {
      path: '/outputs',
      name: 'Outputs',
      component: () => import('@/views/Outputs.vue'),
    },
    {
      path: '/pipes',
      name: 'Pipes',
      component: () => import('@/views/Pipes.vue'),
    },
    {
      path: '/logs',
      name: 'Logs',
      component: () => import('@/views/Logs.vue'),
    },
  ],
})

export default router
