<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Toast from "../Toast.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const rangeHours = ref("24");
const machineId = ref("");
const machines = ref([]);
const entries = ref([]);
const summary = ref(null);
const summaryLoading = ref(false);
const entriesLoading = ref(false);
const lastLoadedAt = ref("");

let refreshTimer = null;

function unwrapData(response) {
  if (!response || response["status"] != "success") {
    return null;
  }
  return response["data"] || null;
}

function formatTime(value) {
  if (!value) {
    return "未记录";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }
  return date.toLocaleString();
}

function formatBytes(value) {
  const size = Number(value || 0);
  if (!Number.isFinite(size) || size <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  let idx = 0;
  let current = size;
  while (current >= 1024 && idx < units.length - 1) {
    current = current / 1024;
    idx += 1;
  }
  return `${current.toFixed(current >= 10 || idx == 0 ? 0 : 1)} ${units[idx]}`;
}

function formatCount(value) {
  return Number(value || 0).toLocaleString();
}

function exportStatusLabel(entry) {
  if (entry["exportedAt"]) {
    return "已导出";
  }
  if (entry["exportError"]) {
    return "失败";
  }
  return "待导出";
}

function exportStatusClass(entry) {
  if (entry["exportedAt"]) {
    return "bg-green-50 text-green-700 border-green-100";
  }
  if (entry["exportError"]) {
    return "bg-red-50 text-red-700 border-red-100";
  }
  return "bg-amber-50 text-amber-700 border-amber-100";
}

function currentFilters() {
  const hours = Number(rangeHours.value || 24);
  const query = {
    from: new Date(Date.now() - hours * 60 * 60 * 1000).toISOString(),
    limit: "20",
  };
  if (machineId.value) {
    query["machine_id"] = String(machineId.value);
  }
  return query;
}

function buildQueryString() {
  return new URLSearchParams(currentFilters()).toString();
}

function loadMachines() {
  return axios
    .get("/admin/api/machines")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取设备列表失败");
      }
      machines.value = Array.isArray(payload["machines"]) ? payload["machines"] : [];
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    });
}

function loadSummary() {
  summaryLoading.value = true;
  return axios
    .get("/admin/api/flow-logs/summary?" + buildQueryString())
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取流量日志汇总失败");
      }
      summary.value = payload["summary"] || null;
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      summaryLoading.value = false;
    });
}

function loadEntries() {
  entriesLoading.value = true;
  return axios
    .get("/admin/api/flow-logs?" + buildQueryString())
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取流量日志失败");
      }
      entries.value = Array.isArray(payload["entries"]) ? payload["entries"] : [];
      lastLoadedAt.value = new Date().toLocaleString();
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      entriesLoading.value = false;
    });
}

function reloadAll() {
  loadSummary().then().catch();
  loadEntries().then().catch();
}

function downloadExport() {
  window.open("/admin/api/flow-logs/export?" + buildQueryString(), "_blank");
}

const exportSummary = computed(() => summary.value?.export || {});

watch([rangeHours, machineId], () => {
  reloadAll();
});

onMounted(() => {
  loadMachines().then().catch();
  reloadAll();
  refreshTimer = setInterval(function () {
    reloadAll();
  }, 30000);
});

onBeforeUnmount(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
});
</script>

<template>
  <div class="w-full max-w-6xl">
    <div class="mb-8">
      <h1 class="text-3xl font-semibold tracking-tight leading-tight mb-2">流量日志</h1>
      <p class="text-gray-600">查看当前租户最近的 data-plane flow-log 样本、导出状态和 NDJSON 导出。</p>
    </div>

    <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <div>
            <p class="text-sm font-medium text-gray-700 mb-2">时间窗口</p>
            <select v-model="rangeHours" class="select select-bordered w-full">
              <option value="24">最近 24 小时</option>
              <option value="168">最近 7 天</option>
              <option value="720">最近 30 天</option>
            </select>
          </div>
          <div>
            <p class="text-sm font-medium text-gray-700 mb-2">设备</p>
            <select v-model="machineId" class="select select-bordered w-full">
              <option value="">全部设备</option>
              <option v-for="machine in machines" :key="machine.id" :value="machine.id">
                {{ machine.name || machine.givenName || machine.hostname || machine.id }}
              </option>
            </select>
          </div>
        </div>
        <div class="flex gap-3">
          <button class="btn border-stone-200 bg-white" @click="reloadAll">刷新</button>
          <button class="btn border-0 bg-blue-500 hover:bg-blue-900 text-white" @click="downloadExport">
            导出当前窗口
          </button>
        </div>
      </div>

      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 mt-6">
        <div class="rounded-xl bg-stone-50 p-4">
          <div class="text-sm text-gray-500">样本数</div>
          <div class="text-2xl font-semibold mt-1">{{ formatCount(summary?.entryCount) }}</div>
        </div>
        <div class="rounded-xl bg-stone-50 p-4">
          <div class="text-sm text-gray-500">虚拟流量</div>
          <div class="text-sm font-medium mt-2">
            TX {{ formatBytes(summary?.virtualTraffic?.txBytes) }}
          </div>
          <div class="text-sm font-medium mt-1">
            RX {{ formatBytes(summary?.virtualTraffic?.rxBytes) }}
          </div>
        </div>
        <div class="rounded-xl bg-stone-50 p-4">
          <div class="text-sm text-gray-500">导出状态</div>
          <div class="mt-2 flex flex-wrap gap-2 text-sm">
            <span class="rounded-full bg-amber-50 px-3 py-1 text-amber-700">待导出 {{ formatCount(exportSummary.pendingCount) }}</span>
            <span class="rounded-full bg-green-50 px-3 py-1 text-green-700">已导出 {{ formatCount(exportSummary.exportedCount) }}</span>
            <span class="rounded-full bg-red-50 px-3 py-1 text-red-700">失败 {{ formatCount(exportSummary.failedCount) }}</span>
          </div>
        </div>
        <div class="rounded-xl bg-stone-50 p-4">
          <div class="text-sm text-gray-500">最近导出</div>
          <div class="text-sm font-medium mt-2">{{ formatTime(exportSummary.lastExportedAt) }}</div>
          <div class="text-xs text-gray-500 mt-1">最近刷新：{{ lastLoadedAt || "未加载" }}</div>
        </div>
      </div>
    </section>

    <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm mt-6">
      <div class="flex items-start justify-between gap-4 mb-5">
        <div>
          <h2 class="text-lg font-semibold">最近样本</h2>
          <p class="text-sm text-gray-500 mt-1">这里展示租户范围内最近 20 条样本。</p>
        </div>
        <div class="text-sm text-gray-400">{{ summaryLoading || entriesLoading ? "加载中..." : "已更新" }}</div>
      </div>

      <div v-if="entriesLoading" class="text-sm text-gray-400">日志加载中...</div>
      <div v-else-if="entries.length == 0" class="rounded-xl bg-stone-50 px-4 py-8 text-center text-sm text-gray-500">
        当前筛选条件下没有流量日志。
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table w-full">
          <thead>
            <tr>
              <th>时间</th>
              <th>设备</th>
              <th>虚拟流量</th>
              <th>导出状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in entries" :key="entry.id">
              <td class="align-top text-sm text-gray-600">
                <div>{{ formatTime(entry.loggedAt) }}</div>
                <div class="text-xs text-gray-400 mt-1">{{ entry.nodeStableId || "-" }}</div>
              </td>
              <td class="align-top">
                <div class="font-medium text-gray-900">{{ entry.machine?.name || "未知设备" }}</div>
                <div class="text-sm text-gray-500 mt-1">{{ entry.user?.name || "未知用户" }}</div>
              </td>
              <td class="align-top text-sm text-gray-600">
                <div>TX {{ formatBytes(entry.virtualTraffic?.txBytes) }}</div>
                <div class="mt-1">RX {{ formatBytes(entry.virtualTraffic?.rxBytes) }}</div>
              </td>
              <td class="align-top">
                <span class="inline-flex rounded-full border px-3 py-1 text-xs font-medium" :class="exportStatusClass(entry)">
                  {{ exportStatusLabel(entry) }}
                </span>
                <div class="text-xs text-gray-500 mt-2" v-if="entry.exportedAt">成功：{{ formatTime(entry.exportedAt) }}</div>
                <div class="text-xs text-red-500 mt-2 whitespace-pre-wrap break-all" v-else-if="entry.exportError">
                  {{ entry.exportError }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <teleport to=".toast-container">
      <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
    </teleport>
  </div>
</template>

<style scoped></style>
