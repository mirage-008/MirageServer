<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Toast from "../components/Toast.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const configLoading = ref(false);
const summaryLoading = ref(false);
const entriesLoading = ref(false);
const saveText = ref("保存");
const rangeHours = ref("24");
const recentEntries = ref([]);
const summary = ref(null);
const lastLoadedAt = ref("");

const enabled = ref(false);
const logExitFlows = ref(false);
const retentionDays = ref(30);
const exportEnabled = ref(false);
const exportTarget = ref("http");
const exportURL = ref("");
const exportAPIKey = ref("");
const exportIndex = ref("");
const exportBatchSize = ref(100);
const exportInsecureSkipVerify = ref(false);

const supportedExportTargets = ref(["http", "elasticsearch"]);
const exportBatchSizeMax = ref(500);

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
  const from = new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
  return {
    from,
    limit: "20",
  };
}

function buildQueryString() {
  return new URLSearchParams(currentFilters()).toString();
}

function applyConfigPayload(payload) {
  const config = payload?.config || {};
  const exportCfg = config["export"] || {};
  enabled.value = !!config["enabled"];
  logExitFlows.value = !!config["logExitFlows"];
  retentionDays.value = Number(config["retentionDays"] || 30);
  exportEnabled.value = !!exportCfg["enabled"];
  exportTarget.value = exportCfg["target"] || "http";
  exportURL.value = exportCfg["url"] || "";
  exportAPIKey.value = exportCfg["apiKey"] || "";
  exportIndex.value = exportCfg["index"] || "";
  exportBatchSize.value = Number(exportCfg["batchSize"] || 100);
  exportInsecureSkipVerify.value = !!exportCfg["insecureSkipVerify"];
  supportedExportTargets.value = Array.isArray(payload?.supportedExportTargets)
    ? payload.supportedExportTargets
    : ["http", "elasticsearch"];
  exportBatchSizeMax.value = Number(payload?.exportBatchSizeMax || 500);
}

function loadConfig() {
  configLoading.value = true;
  return axios
    .get("/cockpit/api/flow-logs/config")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取流量日志配置失败");
      }
      applyConfigPayload(payload);
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      configLoading.value = false;
    });
}

function loadSummary() {
  summaryLoading.value = true;
  return axios
    .get("/cockpit/api/flow-logs/summary?" + buildQueryString())
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
    .get("/cockpit/api/flow-logs?" + buildQueryString())
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取流量日志失败");
      }
      recentEntries.value = Array.isArray(payload["entries"]) ? payload["entries"] : [];
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

function reloadData() {
  loadSummary().then().catch();
  loadEntries().then().catch();
}

function saveConfig() {
  saveText.value = "保存中...";
  axios
    .post("/cockpit/api/flow-logs/config", {
      enabled: !!enabled.value,
      logExitFlows: !!enabled.value && !!logExitFlows.value,
      retentionDays: Number(retentionDays.value || 30),
      export: {
        enabled: !!exportEnabled.value,
        target: exportTarget.value,
        url: exportURL.value,
        apiKey: exportAPIKey.value,
        index: exportIndex.value,
        batchSize: Number(exportBatchSize.value || 100),
        insecureSkipVerify: !!exportInsecureSkipVerify.value,
      },
    })
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "保存流量日志配置失败");
      }
      applyConfigPayload(payload);
      saveText.value = "已保存!";
      setTimeout(function () {
        saveText.value = "保存";
      }, 1500);
      toastMsg.value = "已更新流量日志配置";
      toastShow.value = true;
      reloadData();
    })
    .catch(function (error) {
      saveText.value = "保存";
      toastMsg.value = String(error);
      toastShow.value = true;
    });
}

function downloadExport() {
  window.open("/cockpit/api/flow-logs/export?" + buildQueryString(), "_blank");
}

const exportSummary = computed(() => summary.value?.export || {});

watch(rangeHours, () => {
  reloadData();
});

onMounted(() => {
  loadConfig().then().catch();
  reloadData();
  refreshTimer = setInterval(function () {
    reloadData();
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
      <p class="text-gray-600">
        配置 Mirage 的 flow-log 收集、异步导出和最近流量观测。当前页面展示的是平台级汇总。
      </p>
    </div>

    <div class="grid gap-6 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
      <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
        <div class="flex items-start justify-between mb-6">
          <div>
            <h2 class="text-lg font-semibold">平台配置</h2>
            <p class="text-sm text-gray-500 mt-1">控制是否接收 flow-log，以及收集后的导出目标。</p>
          </div>
          <span class="text-sm text-gray-400">{{ configLoading ? "加载中..." : "已加载" }}</span>
        </div>

        <div class="space-y-5">
          <label class="flex items-center justify-between rounded-xl border border-stone-200 px-4 py-3">
            <div>
              <div class="font-medium text-gray-900">启用流量日志</div>
              <div class="text-sm text-gray-500">允许客户端把 data-plane audit log 上传到 Mirage。</div>
            </div>
            <input v-model="enabled" type="checkbox" class="toggle toggle-primary" />
          </label>

          <label class="flex items-center justify-between rounded-xl border border-stone-200 px-4 py-3">
            <div>
              <div class="font-medium text-gray-900">记录出口流量</div>
              <div class="text-sm text-gray-500">对应 `log-exit-flows` 能力。关闭后只记录虚拟/子网/物理流量。</div>
            </div>
            <input v-model="logExitFlows" :disabled="!enabled" type="checkbox" class="toggle toggle-primary" />
          </label>

          <div>
            <p class="text-sm font-medium text-gray-700 mb-2">保留天数</p>
            <input
              v-model="retentionDays"
              type="number"
              min="1"
              class="input input-bordered w-full max-w-xs"
            />
          </div>

          <div class="rounded-2xl border border-stone-200 p-4 bg-stone-50/60">
            <div class="flex items-center justify-between mb-4">
              <div>
                <h3 class="font-semibold text-gray-900">异步导出</h3>
                <p class="text-sm text-gray-500">把已接收的 flow-log 异步投递到外部 sink。</p>
              </div>
              <input v-model="exportEnabled" type="checkbox" class="toggle toggle-primary" />
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <p class="text-sm font-medium text-gray-700 mb-2">目标类型</p>
                <select v-model="exportTarget" :disabled="!exportEnabled" class="select select-bordered w-full">
                  <option v-for="target in supportedExportTargets" :key="target" :value="target">
                    {{ target }}
                  </option>
                </select>
              </div>
              <div>
                <p class="text-sm font-medium text-gray-700 mb-2">批大小</p>
                <input
                  v-model="exportBatchSize"
                  :disabled="!exportEnabled"
                  type="number"
                  min="1"
                  :max="exportBatchSizeMax"
                  class="input input-bordered w-full"
                />
              </div>
              <div class="md:col-span-2">
                <p class="text-sm font-medium text-gray-700 mb-2">导出地址</p>
                <input
                  v-model="exportURL"
                  :disabled="!exportEnabled"
                  type="text"
                  class="input input-bordered w-full"
                  placeholder="https://collector.example.test/ingest 或 Elasticsearch base URL"
                />
              </div>
              <div>
                <p class="text-sm font-medium text-gray-700 mb-2">API Key / Authorization</p>
                <input
                  v-model="exportAPIKey"
                  :disabled="!exportEnabled"
                  type="text"
                  class="input input-bordered w-full"
                  placeholder="留空则不带鉴权头"
                />
              </div>
              <div>
                <p class="text-sm font-medium text-gray-700 mb-2">索引名</p>
                <input
                  v-model="exportIndex"
                  :disabled="!exportEnabled || exportTarget != 'elasticsearch'"
                  type="text"
                  class="input input-bordered w-full"
                  placeholder="mirage-flow-logs"
                />
              </div>
            </div>

            <label class="flex items-center gap-3 mt-4 text-sm text-gray-600">
              <input
                v-model="exportInsecureSkipVerify"
                :disabled="!exportEnabled"
                type="checkbox"
                class="checkbox checkbox-sm"
              />
              跳过 TLS 证书校验
            </label>
          </div>

          <button class="btn border-0 bg-blue-500 hover:bg-blue-900 text-white" @click="saveConfig">
            {{ saveText }}
          </button>
        </div>
      </section>

      <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
        <div class="flex items-start justify-between mb-6">
          <div>
            <h2 class="text-lg font-semibold">近况概览</h2>
            <p class="text-sm text-gray-500 mt-1">最近时间窗口内的采集量和导出队列健康度。</p>
          </div>
          <div class="flex items-center gap-2">
            <select v-model="rangeHours" class="select select-bordered select-sm">
              <option value="24">最近 24 小时</option>
              <option value="168">最近 7 天</option>
              <option value="720">最近 30 天</option>
            </select>
            <button class="btn btn-sm border-stone-200 bg-white" @click="reloadData">刷新</button>
          </div>
        </div>

        <div v-if="summaryLoading" class="text-sm text-gray-400">汇总加载中...</div>
        <div v-else class="space-y-6">
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="rounded-xl bg-stone-50 p-4">
              <div class="text-sm text-gray-500">样本数</div>
              <div class="text-2xl font-semibold mt-1">{{ formatCount(summary?.entryCount) }}</div>
            </div>
            <div class="rounded-xl bg-stone-50 p-4">
              <div class="text-sm text-gray-500">虚拟流量</div>
              <div class="text-2xl font-semibold mt-1">
                {{ formatBytes(summary?.virtualTraffic?.txBytes) }} / {{ formatBytes(summary?.virtualTraffic?.rxBytes) }}
              </div>
              <div class="text-xs text-gray-500 mt-1">TX / RX</div>
            </div>
            <div class="rounded-xl bg-stone-50 p-4">
              <div class="text-sm text-gray-500">导出队列</div>
              <div class="mt-2 flex flex-wrap gap-2 text-sm">
                <span class="rounded-full bg-amber-50 px-3 py-1 text-amber-700">待导出 {{ formatCount(exportSummary.pendingCount) }}</span>
                <span class="rounded-full bg-green-50 px-3 py-1 text-green-700">已导出 {{ formatCount(exportSummary.exportedCount) }}</span>
                <span class="rounded-full bg-red-50 px-3 py-1 text-red-700">失败 {{ formatCount(exportSummary.failedCount) }}</span>
              </div>
            </div>
            <div class="rounded-xl bg-stone-50 p-4">
              <div class="text-sm text-gray-500">最近导出</div>
              <div class="text-sm font-medium text-gray-900 mt-2">{{ formatTime(exportSummary.lastExportedAt) }}</div>
              <div class="text-xs text-gray-500 mt-1">最近尝试：{{ formatTime(exportSummary.lastAttemptAt) }}</div>
            </div>
          </div>

          <div class="rounded-xl border border-stone-200 p-4">
            <div class="text-sm text-gray-500">数据区间</div>
            <div class="font-medium mt-2">{{ formatTime(summary?.start) }} 至 {{ formatTime(summary?.end) }}</div>
            <div class="text-xs text-gray-500 mt-2">最近刷新：{{ lastLoadedAt || "未加载" }}</div>
          </div>
        </div>
      </section>
    </div>

    <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm mt-6">
      <div class="flex items-start justify-between mb-5 gap-4">
        <div>
          <h2 class="text-lg font-semibold">最近样本</h2>
          <p class="text-sm text-gray-500 mt-1">只展示最近 20 条。完整原始数据可直接导出为 NDJSON。</p>
        </div>
        <button class="btn border-stone-200 bg-white" @click="downloadExport">导出当前窗口</button>
      </div>

      <div v-if="entriesLoading" class="text-sm text-gray-400">日志加载中...</div>
      <div v-else-if="recentEntries.length == 0" class="rounded-xl bg-stone-50 px-4 py-8 text-center text-sm text-gray-500">
        当前时间窗口没有采集到流量日志。
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table w-full">
          <thead>
            <tr>
              <th>时间</th>
              <th>组织 / 设备</th>
              <th>虚拟流量</th>
              <th>导出状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in recentEntries" :key="entry.id">
              <td class="align-top text-sm text-gray-600">
                <div>{{ formatTime(entry.loggedAt) }}</div>
                <div class="text-xs text-gray-400 mt-1">{{ entry.nodeStableId || "-" }}</div>
              </td>
              <td class="align-top">
                <div class="font-medium text-gray-900">{{ entry.org?.name || "未知组织" }}</div>
                <div class="text-sm text-gray-500 mt-1">{{ entry.machine?.name || "未知设备" }}</div>
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
