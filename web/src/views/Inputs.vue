<template>
  <div class="inputs-view">
    <div class="page-header">
      <h2>输入端管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建输入端</el-button>
    </div>

    <el-table :data="inputStore.inputs" v-loading="inputStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column prop="name" label="名称" />
      <el-table-column prop="type" label="类型" width="100">
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

    <el-dialog v-model="createDialogVisible" title="创建输入端" width="500">
      <el-form :model="createForm" label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="createForm.name" placeholder="请输入名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="createForm.type" placeholder="请选择类型">
            <el-option label="本地文件" value="file" />
            <el-option label="RTSP流" value="rtsp" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="createForm.type === 'file'" label="文件路径">
          <el-input v-model="createForm.configPath" placeholder="/path/to/video.mp4" />
        </el-form-item>
        <el-form-item v-if="createForm.type === 'rtsp'" label="RTSP URL">
          <el-input v-model="createForm.configUrl" placeholder="rtsp://example.com/stream" />
        </el-form-item>
        <el-form-item v-if="createForm.type === 'file'" label="循环播放">
          <el-switch v-model="createForm.configLoop" />
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
import { useInputStore } from '@/stores/input'

const inputStore = useInputStore()
const createDialogVisible = ref(false)

const createForm = reactive({
  name: '',
  type: 'file',
  configPath: '',
  configUrl: '',
  configLoop: true,
})

onMounted(() => {
  inputStore.fetchInputs()
})

function getTypeTag(type: string) {
  const tags: Record<string, string> = {
    file: '',
    rtsp: 'warning',
    rtmp: 'danger',
    hls: 'success',
  }
  return tags[type] || 'info'
}

function showCreateDialog() {
  createForm.name = ''
  createForm.type = 'file'
  createForm.configPath = ''
  createForm.configUrl = ''
  createForm.configLoop = true
  createDialogVisible.value = true
}

async function handleCreate() {
  if (!createForm.name) {
    ElMessage.warning('请输入名称')
    return
  }

  let config = ''
  if (createForm.type === 'file') {
    if (!createForm.configPath) {
      ElMessage.warning('请输入文件路径')
      return
    }
    config = JSON.stringify({ path: createForm.configPath, loop: createForm.configLoop })
  } else if (createForm.type === 'rtsp') {
    if (!createForm.configUrl) {
      ElMessage.warning('请输入RTSP URL')
      return
    }
    config = JSON.stringify({ url: createForm.configUrl, transport: 'tcp' })
  }

  try {
    await inputStore.createInput(createForm.name, createForm.type, config)
    ElMessage.success('创建成功')
    createDialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '创建失败')
  }
}

async function handleStart(id: string) {
  try {
    await inputStore.startInput(id)
    ElMessage.success('启动成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '启动失败')
  }
}

async function handleStop(id: string) {
  try {
    await inputStore.stopInput(id)
    ElMessage.success('停止成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '停止失败')
  }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该输入端吗？', '确认')
    await inputStore.deleteInput(id)
    ElMessage.success('删除成功')
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '删除失败')
    }
  }
}
</script>

<style scoped>
.inputs-view {
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
