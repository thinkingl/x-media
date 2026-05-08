<template>
  <div class="logs-view">
    <div class="page-header">
      <h2>日志管理</h2>
      <div class="header-actions">
        <el-select v-model="logLevel" placeholder="日志级别" style="width: 120px; margin-right: 10px;">
          <el-option label="全部" value="" />
          <el-option label="DEBUG" value="debug" />
          <el-option label="INFO" value="info" />
          <el-option label="WARN" value="warn" />
          <el-option label="ERROR" value="error" />
        </el-select>
        <el-button type="primary" @click="refreshLogs">刷新</el-button>
      </div>
    </div>

    <el-card shadow="never">
      <div class="log-container" ref="logContainer">
        <div
          v-for="(log, index) in filteredLogs"
          :key="index"
          :class="['log-entry', `log-${log.level}`]"
        >
          <span class="log-timestamp">{{ log.timestamp }}</span>
          <span class="log-level">{{ log.level.toUpperCase() }}</span>
          <span class="log-message">{{ log.message }}</span>
        </div>
        <div v-if="filteredLogs.length === 0" class="no-logs">暂无日志</div>
      </div>
    </el-card>

    <el-card shadow="never" style="margin-top: 20px;">
      <template #header>
        <div class="card-header">
          <span>日志配置</span>
        </div>
      </template>
      <el-form v-if="logConfig" :model="logConfig" label-width="120px">
        <el-form-item label="日志级别">
          <el-select v-model="logConfig.level">
            <el-option label="DEBUG" value="debug" />
            <el-option label="INFO" value="info" />
            <el-option label="WARN" value="warn" />
            <el-option label="ERROR" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item label="日志文件">
          <el-input v-model="logConfig.filename" />
        </el-form-item>
        <el-form-item label="最大大小(MB)">
          <el-input-number v-model="logConfig.max_size" :min="1" :max="1000" />
        </el-form-item>
        <el-form-item label="最大备份数">
          <el-input-number v-model="logConfig.max_backups" :min="0" :max="100" />
        </el-form-item>
        <el-form-item label="最大保留天数">
          <el-input-number v-model="logConfig.max_age" :min="1" :max="365" />
        </el-form-item>
        <el-form-item label="压缩">
          <el-switch v-model="logConfig.compress" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="handleUpdateConfig">保存配置</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useLogStore } from '@/stores/log'

const logStore = useLogStore()
const logLevel = ref('')
const logContainer = ref<HTMLElement>()

const filteredLogs = computed(() => {
  if (!logLevel.value) return logStore.logs
  return logStore.logs.filter((log) => log.level === logLevel.value)
})

const logConfig = computed(() => logStore.logConfig)

onMounted(() => {
  logStore.fetchLogs(200)
  logStore.fetchLogConfig()
})

function refreshLogs() {
  logStore.fetchLogs(200)
}

async function handleUpdateConfig() {
  if (!logConfig.value) return
  try {
    await logStore.updateLogConfig(logConfig.value)
    ElMessage.success('配置已更新')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '更新失败')
  }
}
</script>

<style scoped>
.logs-view {
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.page-header h2 {
  color: #303133;
}

.header-actions {
  display: flex;
  align-items: center;
}

.log-container {
  max-height: 500px;
  overflow-y: auto;
  font-family: 'Courier New', Courier, monospace;
  font-size: 13px;
  background-color: #1e1e1e;
  color: #d4d4d4;
  padding: 10px;
  border-radius: 4px;
}

.log-entry {
  padding: 2px 0;
  line-height: 1.5;
}

.log-timestamp {
  color: #6a9955;
  margin-right: 10px;
}

.log-level {
  display: inline-block;
  width: 50px;
  margin-right: 10px;
  font-weight: bold;
}

.log-debug .log-level {
  color: #6a9955;
}

.log-info .log-level {
  color: #569cd6;
}

.log-warn .log-level {
  color: #dcdcaa;
}

.log-error .log-level {
  color: #f44747;
}

.log-message {
  color: #d4d4d4;
}

.no-logs {
  text-align: center;
  color: #6a9955;
  padding: 20px;
}

.card-header {
  font-weight: bold;
}
</style>
