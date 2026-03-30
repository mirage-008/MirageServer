<script setup>
import { computed, onMounted, ref, watch } from "vue";
import Toast from "./Toast.vue";

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const domains = ref([]);
const services = ref([]);
const machines = ref([]);

const domainForm = ref({
  domainType: "managed",
  domain: "",
  listenerMode: "direct",
  edgeMode: "server_edge",
});

const serviceForm = ref({
  machineId: "",
  domainMode: "managed",
  domainId: "",
  listenProto: "https",
  listenPort: "",
  mountPath: "/",
  backendType: "http_proxy",
  backendScheme: "http",
  backendPort: "",
  enabled: true,
});

const domainSubmitting = ref(false);
const serviceSubmitting = ref(false);
const reloading = ref(false);
const deletingDomainID = ref("");
const verifyingDomainID = ref("");
const togglingServiceID = ref("");
const deletingServiceID = ref("");

const availableDomains = computed(() => {
  return domains.value;
});

const canConfigureBackendScheme = computed(() => {
  return serviceForm.value.backendType == "http_proxy";
});

function normalizeServiceRecord(item) {
  if (!item) {
    return {};
  }
  if (item["service"]) {
    const service = item["service"] || {};
    const domain = item["domain"] || {};
    const cert = item["cert"] || {};
    const currentEdge = item["currentEdge"] || {};
    return {
      ...service,
      domain: domain["domain"] || service["domain"],
      domainStatus: domain["status"],
      dnsStatus: domain["dnsStatus"] || domain["dns_status"],
      certStatus: cert["certStatus"] || cert["cert_status"],
      currentEdge,
      currentEdgeName: currentEdge["hostname"] || currentEdge["stableId"] || "",
      lastError: item["lastError"] || service["lastError"] || service["last_error"] || "",
      summaryStatus: item["summaryStatus"] || "",
      summaryLabel: item["summaryLabel"] || "",
      summaryReason: item["summaryReason"] || "",
      nextAction: item["nextAction"] || "",
      publicEndpoint: item["publicEndpoint"] || "",
    };
  }
  return item;
}

function unwrapData(response) {
  if (!response || response["status"] != "success") {
    return null;
  }
  return response["data"] || null;
}

function requestErrorMessage(error, fallback) {
  const responseStatus = error?.response?.data?.status;
  if (typeof responseStatus == "string" && responseStatus.trim() != "") {
    return normalizeRequestErrorMessage(responseStatus, fallback);
  }
  if (typeof error?.message == "string" && error.message.trim() != "") {
    return normalizeRequestErrorMessage(error.message, fallback);
  }
  return normalizeRequestErrorMessage(String(error || ""), fallback);
}

function showRequestError(error, fallback) {
  toastMsg.value = requestErrorMessage(error, fallback);
  toastShow.value = true;
}

function isInternalErrorDetail(value) {
  const text = String(value || "").toLowerCase();
  if (text == "") {
    return false;
  }
  return [
    "json:",
    "sql:",
    "sqlite",
    "gorm",
    "decode failed",
    "unmarshal",
    "marshal",
    "dnsmgr",
    "record not found",
    "unexpected eof",
    "stream closed",
    "x509",
    "tls:",
    "lookup ",
    "dial ",
    "http2:",
    "cannot ",
  ].some(function (keyword) {
    return text.includes(keyword);
  });
}

function normalizeRequestErrorMessage(value, fallback) {
  let message = String(value || "")
    .replace(/^error-/, "")
    .replace(/^Error:\s*/, "")
    .replace(/Funnel/g, "")
    .trim();
  if (message == "") {
    return fallback;
  }
  const parts = message.split(/[:：]/);
  if (parts.length > 1) {
    const prefix = parts.shift()?.trim() || fallback;
    const detail = parts.join("：").trim();
    if (detail == "") {
      return prefix;
    }
    if (isInternalErrorDetail(detail)) {
      return prefix;
    }
    return `${prefix}：${detail}`;
  }
  if (isInternalErrorDetail(message)) {
    return fallback;
  }
  return message;
}

function servicePayloadFromForm() {
  const payload = {
    machineId: String(serviceForm.value.machineId || ""),
    domainMode: serviceForm.value.domainMode,
    listenProto: serviceForm.value.listenProto,
    backendType: serviceForm.value.backendType,
    backendPort: Number(serviceForm.value.backendPort),
    mountPath: serviceForm.value.mountPath || "/",
    enabled: !!serviceForm.value.enabled,
  };

  if (serviceForm.value.domainMode == "existing") {
    payload["domainMode"] = "existing";
    payload["domainId"] = String(serviceForm.value.domainId || "");
  }
  if (serviceForm.value.listenPort !== "") {
    payload["listenPort"] = Number(serviceForm.value.listenPort);
  }
  if (serviceForm.value.backendType == "http_proxy") {
    payload["backendScheme"] = serviceForm.value.backendScheme;
  }
  return payload;
}

function loadDomains() {
  return axios
    .get("/admin/api/funnel/domains")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取公网域名失败");
      }
      domains.value = Array.isArray(payload["domains"]) ? payload["domains"] : [];
    })
    .catch(function (error) {
      showRequestError(error, "获取公网域名失败");
    });
}

function loadServices() {
  return axios
    .get("/admin/api/funnel/services")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取公网服务失败");
      }
      services.value = Array.isArray(payload["services"])
        ? payload["services"].map(normalizeServiceRecord)
        : [];
    })
    .catch(function (error) {
      showRequestError(error, "获取公网服务失败");
    });
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
      if (!serviceForm.value.machineId && machines.value.length > 0) {
        serviceForm.value.machineId = machines.value[0]["id"];
      }
    })
    .catch(function (error) {
      showRequestError(error, "获取设备列表失败");
    });
}

function reloadAll() {
  loadDomains().then().catch();
  loadServices().then().catch();
  loadMachines().then().catch();
}

function refreshPage() {
  reloading.value = true;
  Promise.all([loadDomains(), loadServices(), loadMachines()]).finally(function () {
    reloading.value = false;
  });
}

function createDomain() {
  if (domainForm.value.domainType == "custom" && !domainForm.value.domain.trim()) {
    toastMsg.value = "请输入自定义域名";
    toastShow.value = true;
    return;
  }

  domainSubmitting.value = true;
  axios
    .post("/admin/api/funnel/domains", {
      domain: domainForm.value.domain.trim(),
      domainType: domainForm.value.domainType,
      listenerMode: domainForm.value.listenerMode,
      edgeMode: domainForm.value.edgeMode,
      tlsMode: "platform_managed",
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "创建公网域名失败");
      }
      const payload = unwrapData(response.data) || {};
      domainForm.value.domain = "";
      toastMsg.value =
        payload?.domain?.domain ||
        (domainForm.value.domainType == "managed" ? "已分配托管域名" : "已创建公网域名");
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "创建域名失败");
    })
    .finally(function () {
      domainSubmitting.value = false;
    });
}

function verifyDomain(domain) {
  verifyingDomainID.value = domain["id"] || domain["stableId"] || "";
  axios
    .post(`/admin/api/funnel/domains/${domain["id"]}/verify`, {
      id: domain["id"],
      domainId: domain["id"],
      stableId: domain["stableId"],
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "域名验证请求失败");
      }
      const payload = unwrapData(response.data) || {};
      toastMsg.value =
        payload["verificationMessage"] ||
        (payload["verified"] === false ? "域名 DNS 当前未就绪" : "域名验证完成");
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "验证域名失败");
    })
    .finally(function () {
      verifyingDomainID.value = "";
    });
}

function deleteDomain(domain) {
  deletingDomainID.value = domain["id"] || domain["stableId"] || "";
  axios
    .delete(`/admin/api/funnel/domains/${domain["id"]}`)
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "删除公网域名失败");
      }
      toastMsg.value = "已删除公网域名";
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "删除域名失败");
    })
    .finally(function () {
      deletingDomainID.value = "";
    });
}

function createService() {
  if (!serviceForm.value.machineId) {
    toastMsg.value = "请选择设备";
    toastShow.value = true;
    return;
  }
  if (!serviceForm.value.backendPort) {
    toastMsg.value = "请输入后端端口";
    toastShow.value = true;
    return;
  }
  if (serviceForm.value.domainMode == "existing" && !serviceForm.value.domainId) {
    toastMsg.value = "请选择已有域名";
    toastShow.value = true;
    return;
  }

  serviceSubmitting.value = true;
  axios
    .post("/admin/api/funnel/services", servicePayloadFromForm())
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "创建公网服务失败");
      }
      toastMsg.value = "已创建公网服务";
      toastShow.value = true;
      serviceForm.value.backendPort = "";
      serviceForm.value.listenPort = "";
      serviceForm.value.mountPath = "/";
      loadDomains().then().catch();
      loadServices().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "创建服务失败");
    })
    .finally(function () {
      serviceSubmitting.value = false;
    });
}

function setServiceEnabled(service, enabled) {
  togglingServiceID.value = service["id"] || service["stableId"] || "";
  axios
    .post(`/admin/api/funnel/services/${service["id"]}/${enabled ? "enable" : "disable"}`)
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "更新公网服务状态失败");
      }
      toastMsg.value = enabled ? "已启用公网服务" : "已停用公网服务";
      toastShow.value = true;
      loadServices().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "更新服务状态失败");
    })
    .finally(function () {
      togglingServiceID.value = "";
    });
}

function deleteService(service) {
  deletingServiceID.value = service["id"] || service["stableId"] || "";
  axios
    .delete(`/admin/api/funnel/services/${service["id"]}`)
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "删除公网服务失败");
      }
      toastMsg.value = "已删除公网服务";
      toastShow.value = true;
      loadServices().then().catch();
    })
    .catch(function (error) {
      showRequestError(error, "删除服务失败");
    })
    .finally(function () {
      deletingServiceID.value = "";
    });
}

function summaryTone(status) {
  switch (status) {
    case "ready":
      return "inline-flex items-center align-middle justify-center font-medium border border-emerald-200 bg-emerald-50 text-emerald-700 rounded-full px-2 py-1 leading-none text-xs";
    case "pending":
      return "inline-flex items-center align-middle justify-center font-medium border border-amber-200 bg-amber-50 text-amber-700 rounded-full px-2 py-1 leading-none text-xs";
    case "error":
      return "inline-flex items-center align-middle justify-center font-medium border border-rose-200 bg-rose-50 text-rose-700 rounded-full px-2 py-1 leading-none text-xs";
    case "disabled":
      return "inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-100 text-stone-700 rounded-full px-2 py-1 leading-none text-xs";
    default:
      return "inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-100 text-stone-700 rounded-full px-2 py-1 leading-none text-xs";
  }
}

function serviceRawStateText(service) {
  return [
    service["configStatus"] || service["config_status"] || "-",
    service["dnsStatus"] || service["dns_status"] || "-",
    service["certStatus"] || service["cert_status"] || "-",
    service["edgeStatus"] || service["edge_status"] || "-",
    service["backendStatus"] || service["backend_status"] || "-",
  ].join(" / ");
}

function serviceSummaryLabel(service) {
  return service["summaryLabel"] || service["configStatus"] || "-";
}

function serviceSummaryReason(service) {
  return service["summaryReason"] || service["lastError"] || service["last_error"] || "-";
}

function serviceNextAction(service) {
  return service["nextAction"] || service["lastError"] || service["last_error"] || "-";
}

function domainSummaryLabel(domain) {
  return domain["summaryLabel"] || domain["status"] || "-";
}

function domainSummaryReason(domain) {
  return domain["summaryReason"] || domain["lastDnsError"] || domain["last_dns_error"] || "等待域名验证完成";
}

function machineLabel(machine) {
  return machine["name"] || machine["hostname"] || machine["id"];
}

function domainTypeLabel(domain) {
  return domain["domainType"] == "managed" ? "托管域名" : "自定义域名";
}

function listenerModeLabel(domain) {
  return domain["listenerMode"] == "behind_proxy" ? "前置代理" : "直接监听";
}

function edgeModeLabel(domain) {
  return domain["edgeMode"] == "remote_edge" ? "Remote Edge" : "MirageServer";
}

onMounted(() => {
  reloadAll();
});
</script>

<template>
  <main class="container mx-auto pb-20 md:pb-24">
    <section class="space-y-10">
      <header class="mb-2 flex items-center">
        <div class="flex items-center">
          <h1 class="text-3xl font-semibold tracking-tight leading-tight mb-2">公网服务</h1>
        </div>
        <div class="ml-auto flex items-center gap-3">
          <div
            class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-gray-600 rounded-full px-2 py-1 leading-none text-sm min-w-fit h-7"
          >
            {{ services.length }} 个服务
          </div>
          <button
            @click="refreshPage"
            :disabled="reloading"
            class="btn h-9 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
          >
            {{ reloading ? "刷新中..." : "刷新" }}
          </button>
        </div>
      </header>

      <section class="grid gap-8 lg:grid-cols-2">
        <div class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
          <header>
            <h2 class="text-xl font-semibold tracking-tight">公共域名</h2>
            <p class="mt-2 text-sm text-gray-500">先准备域名，再绑定服务。免费域名会自动分配，自定义域名需要先完成解析。</p>
          </header>

          <div class="mt-5 space-y-4">
            <div>
              <label class="text-sm text-gray-600">域名类型</label>
              <select
                v-model="domainForm.domainType"
                class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
              >
                <option value="managed">免费托管域名</option>
                <option value="custom">自定义域名</option>
              </select>
            </div>
            <div>
              <label class="text-sm text-gray-600">域名</label>
              <input
                v-if="domainForm.domainType == 'custom'"
                v-model="domainForm.domain"
                class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
                placeholder="app.example.com"
              />
              <div
                v-else
                class="mt-2 rounded-md border border-stone-200 bg-stone-50 px-3 py-3 text-sm text-stone-600"
              >
                将自动分配一个
                <code class="bg-stone-200 rounded px-1">*.mirage.mm.md</code>
                域名。
              </div>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="text-sm text-gray-600">监听模式</label>
                <select
                  v-model="domainForm.listenerMode"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="direct">直接监听</option>
                  <option value="behind_proxy">前置代理</option>
                </select>
              </div>
              <div>
                <label class="text-sm text-gray-600">入口模式</label>
                <select
                  v-model="domainForm.edgeMode"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="server_edge">MirageServer</option>
                  <option value="remote_edge">Remote Edge</option>
                </select>
              </div>
            </div>
            <div>
              <button
                @click="createDomain"
                :disabled="domainSubmitting"
                class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
              >
                {{ domainSubmitting ? "创建中..." : domainForm.domainType == "managed" ? "分配托管域名" : "添加自定义域名" }}
              </button>
            </div>
          </div>

          <div class="mt-6 overflow-x-auto border border-stone-200 rounded-xl">
            <table class="table w-full">
              <thead>
                <tr>
                  <th>域名</th>
                  <th>状态</th>
                <th>说明</th>
                  <th class="text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="domain in domains" :key="domain.id || domain.stableId || domain.domain">
                  <td>
                    <div class="font-semibold text-gray-900">{{ domain.domain }}</div>
                    <div class="text-xs text-gray-400 font-mono">{{ domain.stableId || "-" }}</div>
                  </td>
                  <td>
                    <span :class="summaryTone(domain.summaryStatus)">
                      {{ domainSummaryLabel(domain) }}
                    </span>
                    <div class="text-xs text-gray-500 mt-2">{{ domainSummaryReason(domain) }}</div>
                    <div class="text-xs text-gray-400 mt-1">
                      域名: {{ domain.status || "-" }} / DNS: {{ domain.dnsStatus || domain.dns_status || "-" }}
                    </div>
                  </td>
                  <td class="text-xs text-gray-500">
                    <div class="flex flex-wrap gap-1">
                      <span class="inline-flex items-center rounded-full border border-stone-200 bg-stone-100 px-2 py-1 text-[11px] text-stone-700">
                        {{ domainTypeLabel(domain) }}
                      </span>
                      <span class="inline-flex items-center rounded-full border border-stone-200 bg-stone-100 px-2 py-1 text-[11px] text-stone-700">
                        {{ listenerModeLabel(domain) }}
                      </span>
                      <span class="inline-flex items-center rounded-full border border-stone-200 bg-stone-100 px-2 py-1 text-[11px] text-stone-700">
                        {{ edgeModeLabel(domain) }}
                      </span>
                    </div>
                    <div v-if="domain.validationMethod" class="mt-2 font-mono">{{ domain.validationMethod }}</div>
                    <div v-if="domain.validationTarget" class="mt-1 font-mono">{{ domain.validationTarget }}</div>
                    <div v-if="domain.validationToken" class="mt-1 break-all font-mono">{{ domain.validationToken }}</div>
                  </td>
                  <td>
                    <div class="flex justify-end gap-2">
                      <button
                        @click="verifyDomain(domain)"
                        :disabled="verifyingDomainID == (domain.id || domain.stableId || '')"
                        class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                      >
                        {{ verifyingDomainID == (domain.id || domain.stableId || "") ? "验证中..." : "验证" }}
                      </button>
                      <button
                        @click="deleteDomain(domain)"
                        :disabled="deletingDomainID == (domain.id || domain.stableId || '')"
                        class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                      >
                        {{ deletingDomainID == (domain.id || domain.stableId || "") ? "删除中..." : "删除" }}
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="domains.length == 0">
                  <td colspan="4" class="text-center text-sm text-gray-400 py-6">当前还没有公网域名</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
          <header>
            <h2 class="text-xl font-semibold tracking-tight">公共服务</h2>
            <p class="mt-2 text-sm text-gray-500">把设备上的端口发布到公网。先选设备和域名，再填写协议和后端端口。</p>
          </header>

          <div class="mt-5 space-y-4">
            <div>
              <label class="text-sm text-gray-600">设备</label>
              <select
                v-model="serviceForm.machineId"
                class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
              >
                <option value="" disabled>请选择设备</option>
                <option v-for="machine in machines" :key="machine.id" :value="machine.id">
                  {{ machineLabel(machine) }}
                </option>
              </select>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="text-sm text-gray-600">域名来源</label>
                <select
                  v-model="serviceForm.domainMode"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="managed">新建托管域名</option>
                  <option value="existing">使用已有域名</option>
                </select>
              </div>
              <div v-if="serviceForm.domainMode == 'existing'">
                <label class="text-sm text-gray-600">已有域名</label>
                <select
                  v-model="serviceForm.domainId"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="" disabled>请选择域名</option>
                  <option v-for="domain in availableDomains" :key="domain.id" :value="domain.id">
                    {{ domain.domain }}
                  </option>
                </select>
              </div>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="text-sm text-gray-600">协议</label>
                <select
                  v-model="serviceForm.listenProto"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="http">HTTP</option>
                  <option value="https">HTTPS</option>
                  <option value="ws">WS</option>
                  <option value="wss">WSS</option>
                  <option value="tcp">TCP</option>
                  <option value="tls_terminated_tcp">TLS 终止 TCP</option>
                </select>
              </div>
              <div>
                <label class="text-sm text-gray-600">监听端口</label>
                <input
                  v-model="serviceForm.listenPort"
                  class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
                  placeholder="HTTP/HTTPS 可留空"
                />
              </div>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="text-sm text-gray-600">后端类型</label>
                <select
                  v-model="serviceForm.backendType"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="http_proxy">HTTP 反代</option>
                  <option value="tcp_proxy">TCP 透传</option>
                </select>
              </div>
              <div>
                <label class="text-sm text-gray-600">后端端口</label>
                <input
                  v-model="serviceForm.backendPort"
                  class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
                  placeholder="8080"
                />
              </div>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="text-sm text-gray-600">挂载路径</label>
                <input
                  v-model="serviceForm.mountPath"
                  class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
                  placeholder="/"
                />
              </div>
              <div v-if="canConfigureBackendScheme">
                <label class="text-sm text-gray-600">后端协议</label>
                <select
                  v-model="serviceForm.backendScheme"
                  class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
                >
                  <option value="http">http</option>
                  <option value="https">https</option>
                </select>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <button
                @click="createService"
                :disabled="serviceSubmitting"
                class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
              >
                {{ serviceSubmitting ? "创建中..." : "创建服务" }}
              </button>
              <label class="inline-flex items-center gap-2 text-sm text-gray-600">
                <input v-model="serviceForm.enabled" type="checkbox" class="toggle toggle-sm" />
                创建后立即启用
              </label>
            </div>
          </div>
        </div>
      </section>

      <section class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
        <header>
          <h2 class="text-xl font-semibold tracking-tight">已发布服务</h2>
          <p class="mt-2 text-sm text-gray-500">查看当前发布状态。出问题时会直接告诉你原因和处理办法。</p>
        </header>
        <div class="mt-5 overflow-x-auto border border-stone-200 rounded-xl">
          <table class="table w-full">
            <thead>
              <tr>
                <th>服务</th>
                <th>域名</th>
                <th>后端</th>
                <th>状态</th>
                <th>下一步</th>
                <th class="text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="service in services" :key="service.id || service.stableId">
                <td>
                  <div class="font-semibold text-gray-900">{{ service.listenProto }}:{{ service.listenPort || "-" }}</div>
                  <div class="text-xs text-gray-400 font-mono">{{ service.stableId || "-" }}</div>
                </td>
                <td>
                  <div>{{ service.domain || service.domainName || service.domainId || "-" }}</div>
                  <div class="text-xs text-gray-400">
                    DNS: {{ service.dnsStatus || "-" }} / 证书: {{ service.certStatus || "-" }}
                  </div>
                </td>
                <td class="font-mono text-xs">
                  {{ service.backendScheme || service.backendType }}://{{ service.backendTailnetIp || service.backendTailnetIP || "-" }}:{{ service.backendPort }}
                </td>
                <td class="text-sm">
                  <span :class="summaryTone(service.summaryStatus)">
                    {{ serviceSummaryLabel(service) }}
                  </span>
                  <div class="text-xs text-gray-500 mt-2">{{ serviceSummaryReason(service) }}</div>
                  <div v-if="service.publicEndpoint" class="text-xs font-mono text-gray-400 mt-1">{{ service.publicEndpoint }}</div>
                  <div class="text-xs text-gray-400 mt-1">{{ serviceRawStateText(service) }}</div>
                </td>
                <td class="text-sm text-stone-600">{{ serviceNextAction(service) }}</td>
                <td>
                  <div class="flex justify-end gap-2">
                    <button
                      v-if="!service.enabled"
                      @click="setServiceEnabled(service, true)"
                      :disabled="togglingServiceID == (service.id || service.stableId || '')"
                      class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                    >
                      {{ togglingServiceID == (service.id || service.stableId || "") ? "处理中..." : "启用" }}
                    </button>
                    <button
                      v-else
                      @click="setServiceEnabled(service, false)"
                      :disabled="togglingServiceID == (service.id || service.stableId || '')"
                      class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                    >
                      {{ togglingServiceID == (service.id || service.stableId || "") ? "处理中..." : "停用" }}
                    </button>
                    <button
                      @click="deleteService(service)"
                      :disabled="deletingServiceID == (service.id || service.stableId || '')"
                      class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                    >
                      {{ deletingServiceID == (service.id || service.stableId || "") ? "删除中..." : "删除" }}
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="services.length == 0">
                <td colspan="6" class="text-center text-sm text-gray-400 py-6">当前还没有公网服务</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </section>
  </main>

  <Teleport to="body">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>
