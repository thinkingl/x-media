<template>
  <div class="outputs-view">
    <div class="page-header">
      <h2>输出端管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建输出端</el-button>
    </div>

    <el-table :data="outputStore.outputs" v-loading="outputStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column prop="name" label="名称" />
      <el-table-column prop="type" label="类型" width="120">
        <template #default="{ row }">
          <el-tag :type="getTypeTag(row.type)">{{ row.type }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'running' ? 'success' : 'info'">
            {{ row.status === 'running' ? '运行中' : '已停止' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="250">
        <template #default="{ row }">
          <el-button
            v-if="row.status !== 'running'"
            type="success"
            size="small"
            @click="handleStart(row.id)"
          >
            启动
          </el-button>
          <el-button
            v-if="row.status === 'running'"
            type="warning"
            size="small"
            @click="handleStop(row.id)"
          >
            停止
          </el-button>
          <el-button
            type="danger"
            size="small"
            @click="handleDelete(row.id)"
            :disabled="row.status === 'running'"
          >
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialogVisible" title="创建输出端" width="500">
      <el-form :model="createForm" label-width="120px">
        <el-form-item label="名称">
          <el-input v-model="createForm.name" placeholder="请输入名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="createForm.type" placeholder="请选择类型">
            <el-option label="RTMP" value="rtmp" />
            <el-option label="RTSP" value="rtsp" />
            <el-option label="HTTP-FLV" value="http-flv" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="createForm.type === 'rtmp'" label="RTMP URL">
          <el-input v-model="createForm.configUrl" placeholder="rtmp://live.example.com/live/key" />
        </el-form-item>
        <el-form-item v-if="createForm.type === 'rtsp'" label="模式">
          <el-select v-model="createForm.rtspMode">
            <el-option label="推流" value="push" />
            <el-option label="服务" value="server" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="createForm.type === 'rtsp' && createForm.rtspMode === 'push'" label="RTSP URL">
          <el-input v-model="createForm.configUrl" placeholder="rtsp://example.com/stream" />
        </el-form-item>
        <el-form-item v-if="createForm.type === 'rtsp' && createForm.rtspMode === 'server'" label="监听地址">
          <el-input v-model="createForm.configAddr" placeholder=":5544" />
        </el-form-item>
        <el-form-item v-if="createForm.type === 'http-flv'" label="监听地址">
          <el-input v-model="createForm.configAddr" placeholder=":8080" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleCreate">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useOutputStore } from '@/stores/output'

const outputStore = useOutputStore()
const createDialogVisible = ref(false)

const createForm = reactive({
  name: '',
  type: 'rtmp',
  configUrl: '',
  configAddr: '',
  rtspMode: 'push',
})

onMounted(() => {
  outputStore.fetchOutputs()
})

function getTypeTag(type: string) {
  const tags: Record<string, string> = {
    rtmp: 'danger',
    rtsp: 'warning',
    'http-flv': 'success',
    hls: '',
  }
  return tags[type] || 'info'
}

function showCreateDialog() {
  createForm.name = ''
  createForm.type = 'rtmp'
  createForm.configUrl = ''
  createForm.configAddr = ''
  createForm.rtspMode = 'push'
  createDialogVisible.value = true
}

async function handleCreate() {
  if (!createForm.name) {
    ElMessage.warning('请输入名称')
    return
  }

  let config = ''
  if (createForm.type === 'rtmp') {
    if (!createForm.configUrl) {
      ElMessage.warning('请输入RTMP URL')
      return
    }
    config = JSON.stringify({ url: createForm.configUrl })
  } else if (createForm.type === 'rtsp') {
    if (createForm.rtspMode === 'push') {
      if (!createForm.configUrl) {
        ElMessage.warning('请输入RTSP URL')
        return
      }
      config = JSON.stringify({ mode: 'push', url: createForm.configUrl, transport: 'tcp' })
    } else {
      if (!createForm.configAddr) {
        ElMessage.warning('请输入监听地址')
        return
      }
      config = JSON.stringify({ mode: 'server', addr: createForm.configAddr, transport: 'tcp' })
    }
  } else if (createForm.type === 'http-flv') {
    if (!createForm.configAddr) {
      ElMessage.warning('请输入监听地址')
      return
    }
    config = JSON.stringify({ addr: createForm.configAddr })
  }

  try {
    await outputStore.createOutput(createForm.name, createForm.type, config)
    ElMessage.success('创建成功')
    createDialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '创建失败')
  }
}

async function handleStart(id: string) {
  try {
    await outputStore.startOutput(id)
    ElMessage.success('启动成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '启动失败')
  }
}

async function handleStop(id: string) {
  try {
    await outputStore.stopOutput(id)
    ElMessage.success('停止成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '停止失败')
  }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该输出端吗？', '确认')
    await outputStore.deleteOutput(id)
    ElMessage.success('删除成功')
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '删除失败')
    }
  }
}
</script>

<style scoped>
.outputs-view {
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
</style>
