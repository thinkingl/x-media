<template>
  <div class="pipes-view">
    <div class="page-header">
      <h2>管道管理</h2>
      <el-button type="primary" @click="showCreateDialog">创建管道</el-button>
    </div>

    <el-table :data="pipeStore.pipes" v-loading="pipeStore.loading" stripe>
      <el-table-column prop="id" label="ID" width="120" />
      <el-table-column prop="input_id" label="输入端ID" />
      <el-table-column prop="output_id" label="输出端ID" />
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

    <el-dialog v-model="createDialogVisible" title="创建管道" width="500">
      <el-form :model="createForm" label-width="100px">
        <el-form-item label="输入端">
          <el-select v-model="createForm.inputId" placeholder="请选择输入端">
            <el-option
              v-for="input in inputStore.inputs"
              :key="input.id"
              :label="input.name"
              :value="input.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="输出端">
          <el-select v-model="createForm.outputId" placeholder="请选择输出端">
            <el-option
              v-for="output in outputStore.outputs"
              :key="output.id"
              :label="output.name"
              :value="output.id"
            />
          </el-select>
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
import { usePipeStore } from '@/stores/pipe'
import { useInputStore } from '@/stores/input'
import { useOutputStore } from '@/stores/output'

const pipeStore = usePipeStore()
const inputStore = useInputStore()
const outputStore = useOutputStore()
const createDialogVisible = ref(false)

const createForm = reactive({
  inputId: '',
  outputId: '',
})

onMounted(() => {
  pipeStore.fetchPipes()
  inputStore.fetchInputs()
  outputStore.fetchOutputs()
})

function showCreateDialog() {
  createForm.inputId = ''
  createForm.outputId = ''
  createDialogVisible.value = true
}

async function handleCreate() {
  if (!createForm.inputId) {
    ElMessage.warning('请选择输入端')
    return
  }
  if (!createForm.outputId) {
    ElMessage.warning('请选择输出端')
    return
  }

  try {
    await pipeStore.createPipe(createForm.inputId, createForm.outputId)
    ElMessage.success('创建成功')
    createDialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '创建失败')
  }
}

async function handleStart(id: string) {
  try {
    await pipeStore.startPipe(id)
    ElMessage.success('启动成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '启动失败')
  }
}

async function handleStop(id: string) {
  try {
    await pipeStore.stopPipe(id)
    ElMessage.success('停止成功')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.message || '停止失败')
  }
}

async function handleDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除该管道吗？', '确认')
    await pipeStore.deletePipe(id)
    ElMessage.success('删除成功')
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '删除失败')
    }
  }
}
</script>

<style scoped>
.pipes-view {
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
