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
const deletingDomainID = ref("");
const verifyingDomainID = ref("");
const togglingServiceID = ref("");
const deletingServiceID = ref("");

const activeCustomDomains = computed(() => {
  return domains.value.filter(function (domain) {
    return domain.domainType == "custom";
  });
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

function servicePayloadFromForm() {
  const payload = {
    machineId: Number(serviceForm.value.machineId),
    domainMode: serviceForm.value.domainMode,
    listenProto: serviceForm.value.listenProto,
    backendType: serviceForm.value.backendType,
    backendPort: Number(serviceForm.value.backendPort),
    mountPath: serviceForm.value.mountPath || "/",
    enabled: !!serviceForm.value.enabled,
  };

  if (serviceForm.value.domainMode == "existing") {
    payload["domainMode"] = "custom";
    payload["domainId"] = Number(serviceForm.value.domainId);
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
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
    });
}

function reloadAll() {
  loadDomains().then().catch();
  loadServices().then().catch();
  loadMachines().then().catch();
}

function createDomain() {
  if (!domainForm.value.domain.trim()) {
    toastMsg.value = "请输入自定义域名";
    toastShow.value = true;
    return;
  }

  domainSubmitting.value = true;
  axios
    .post("/admin/api/funnel/domains", {
      domain: domainForm.value.domain.trim(),
      domainType: "custom",
      listenerMode: domainForm.value.listenerMode,
      edgeMode: domainForm.value.edgeMode,
      tlsMode: "platform_managed",
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "创建公网域名失败");
      }
      domainForm.value.domain = "";
      toastMsg.value = "已创建公网域名";
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = "已提交域名验证请求";
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
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
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      deletingServiceID.value = "";
    });
}

function serviceStatusText(service) {
  return [
    service["configStatus"] || service["config_status"] || "-",
    service["dnsStatus"] || service["dns_status"] || "-",
    service["certStatus"] || service["cert_status"] || "-",
    service["edgeStatus"] || service["edge_status"] || "-",
    service["backendStatus"] || service["backend_status"] || "-",
  ].join(" / ");
}

function machineLabel(machine) {
  return machine["name"] || machine["hostname"] || machine["id"];
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
        <div
          class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-200 text-gray-600 rounded-full px-2 py-1 leading-none text-sm ml-4 min-w-fit h-7"
        >
          {{ services.length }} 个服务
        </div>
      </header>

      <section class="grid gap-8 lg:grid-cols-2">
        <div class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
          <header>
            <h2 class="text-xl font-semibold tracking-tight">公共域名</h2>
            <p class="mt-2 text-sm text-gray-500">先添加自定义域名，再完成 DNS 验证；托管域名会在创建服务时自动分配。</p>
          </header>

          <div class="mt-5 space-y-4">
            <div>
              <label class="text-sm text-gray-600">域名</label>
              <input
                v-model="domainForm.domain"
                class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
                placeholder="app.example.com"
              />
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
                {{ domainSubmitting ? "创建中..." : "添加域名" }}
              </button>
            </div>
          </div>

          <div class="mt-6 overflow-x-auto border border-stone-200 rounded-xl">
            <table class="table w-full">
              <thead>
                <tr>
                  <th>域名</th>
                  <th>状态</th>
                  <th>验证信息</th>
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
                    <div>{{ domain.status || "-" }}</div>
                    <div class="text-xs text-gray-400">DNS: {{ domain.dnsStatus || domain.dns_status || "-" }}</div>
                  </td>
                  <td class="text-xs font-mono text-gray-500">
                    <div v-if="domain.validationMethod">{{ domain.validationMethod }}</div>
                    <div v-if="domain.validationTarget">{{ domain.validationTarget }}</div>
                    <div v-if="domain.validationToken">{{ domain.validationToken }}</div>
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
            <p class="mt-2 text-sm text-gray-500">把组织内设备上的服务发布到托管域名，或绑定到已存在的自定义域名。</p>
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
                  <option value="managed">自动分配托管域名</option>
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
                  <option v-for="domain in activeCustomDomains" :key="domain.id" :value="domain.id">
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
          <p class="mt-2 text-sm text-gray-500">这里展示控制面投影状态，便于区分配置已保存、DNS 未就绪、证书待签发、边缘待应用等阶段。</p>
        </header>
        <div class="mt-5 overflow-x-auto border border-stone-200 rounded-xl">
          <table class="table w-full">
            <thead>
              <tr>
                <th>服务</th>
                <th>域名</th>
                <th>后端</th>
                <th>状态</th>
                <th>错误</th>
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
                <td class="text-sm">{{ serviceStatusText(service) }}</td>
                <td class="text-sm text-orange-700">{{ service.lastError || service.last_error || "-" }}</td>
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
