<template>
  <el-container class="app-container">
    <el-header>
      <div class="header-content">
        <h1>X-Media 媒体流处理平台</h1>
        <el-menu
          :default-active="activeMenu"
          mode="horizontal"
          router
          class="header-menu"
        >
          <el-menu-item index="/">仪表盘</el-menu-item>
          <el-menu-item index="/inputs">输入端</el-menu-item>
          <el-menu-item index="/outputs">输出端</el-menu-item>
          <el-menu-item index="/pipes">管道</el-menu-item>
          <el-menu-item index="/test">测试</el-menu-item>
          <el-menu-item index="/logs">日志</el-menu-item>
        </el-menu>
      </div>
    </el-header>
    <el-main>
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const activeMenu = computed(() => route.path)
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
}

.app-container {
  min-height: 100vh;
}

.el-header {
  background-color: #409eff;
  color: white;
  display: flex;
  align-items: center;
  padding: 0 20px;
}

.header-content {
  display: flex;
  align-items: center;
  width: 100%;
}

.header-content h1 {
  font-size: 20px;
  margin-right: 50px;
  white-space: nowrap;
}

.header-menu {
  background-color: transparent;
  border: none;
  flex: 1;
}

.header-menu .el-menu-item {
  color: white !important;
  border-bottom: none !important;
}

.header-menu .el-menu-item:hover {
  background-color: rgba(255, 255, 255, 0.2) !important;
}

.header-menu .el-menu-item.is-active {
  background-color: rgba(255, 255, 255, 0.3) !important;
}

.el-main {
  background-color: #f5f7fa;
  padding: 20px;
}
</style>
