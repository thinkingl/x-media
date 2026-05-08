<template>
  <div class="dashboard">
    <h2>仪表盘</h2>
    <el-row :gutter="20" class="stats-row">
      <el-col :span="8">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>输入端</span>
            </div>
          </template>
          <div class="stat-value">{{ stats?.active_inputs || 0 }}</div>
          <div class="stat-label">活跃数量</div>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>输出端</span>
            </div>
          </template>
          <div class="stat-value">{{ stats?.active_outputs || 0 }}</div>
          <div class="stat-label">活跃数量</div>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>管道</span>
            </div>
          </template>
          <div class="stat-value">{{ stats?.active_pipes || 0 }}</div>
          <div class="stat-label">活跃数量</div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="20" class="stats-row">
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>流量统计</span>
            </div>
          </template>
          <div class="stat-item">
            <span class="stat-label">总接收字节:</span>
            <span class="stat-value">{{ formatBytes(stats?.total_bytes_in || 0) }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">总发送字节:</span>
            <span class="stat-value">{{ formatBytes(stats?.total_bytes_out || 0) }}</span>
          </div>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>快速操作</span>
            </div>
          </template>
          <div class="quick-actions">
            <el-button type="primary" @click="$router.push('/inputs')">管理输入端</el-button>
            <el-button type="success" @click="$router.push('/outputs')">管理输出端</el-button>
            <el-button type="warning" @click="$router.push('/pipes')">管理管道</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { onMounted, computed } from 'vue'
import { useStatsStore } from '@/stores/stats'

const statsStore = useStatsStore()
const stats = computed(() => statsStore.stats)

onMounted(() => {
  statsStore.fetchStats()
})

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}
</script>

<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
}

.dashboard h2 {
  margin-bottom: 20px;
  color: #303133;
}

.stats-row {
  margin-bottom: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.stat-value {
  font-size: 36px;
  font-weight: bold;
  color: #409eff;
  text-align: center;
  margin: 10px 0;
}

.stat-label {
  text-align: center;
  color: #909399;
  font-size: 14px;
}

.stat-item {
  display: flex;
  justify-content: space-between;
  padding: 10px 0;
  border-bottom: 1px solid #ebeef5;
}

.stat-item:last-child {
  border-bottom: none;
}

.stat-item .stat-label {
  text-align: left;
  color: #606266;
}

.stat-item .stat-value {
  font-size: 16px;
  color: #303133;
}

.quick-actions {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.quick-actions .el-button {
  width: 100%;
}
</style>
