<script setup>
import { computed, onMounted, ref, watch } from "vue";
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

const managedBaseDomain = ref("");
const defaultEdgeMode = ref("server_edge");
const defaultListenerMode = ref("direct");
const directBindAddrsText = ref("");
const directBindPortsText = ref("");
const trustedProxyCIDRsText = ref("");
const effectiveIngressTargets = ref([]);
const edges = ref([]);
const domains = ref([]);

const configLoading = ref(false);
const domainsLoading = ref(false);
const saveConfigText = ref("保存");
const verifyingDomainID = ref("");
const renewingDomainID = ref("");

function normalizeListText(value) {
  if (!value) {
    return [];
  }
  return value
    .split(/[\n,]/)
    .map(function (item) {
      return item.trim();
    })
    .filter(function (item) {
      return item != "";
    });
}

function normalizePortList(value) {
  const rawList = normalizeListText(value);
  return rawList
    .map(function (item) {
      return Number(item);
    })
    .filter(function (item) {
      return Number.isInteger(item) && item > 0;
    });
}

function joinList(value) {
  if (!Array.isArray(value) || value.length == 0) {
    return "";
  }
  return value.join(", ");
}

function unwrapData(response) {
  if (!response || response["status"] != "success") {
    return null;
  }
  return response["data"] || null;
}

function applyConfigPayload(payload) {
  const config = payload?.config || payload || {};
  managedBaseDomain.value = config["managedBaseDomain"] || "";
  defaultEdgeMode.value = config["defaultEdgeMode"] || "server_edge";
  defaultListenerMode.value = config["defaultListenerMode"] || "direct";
  directBindAddrsText.value = joinList(config["directBindAddrs"]);
  directBindPortsText.value = joinList(config["directBindPorts"]);
  trustedProxyCIDRsText.value = joinList(config["trustedProxyCIDRs"]);

  if (Array.isArray(payload?.effectiveIngressTargets)) {
    effectiveIngressTargets.value = payload.effectiveIngressTargets;
  } else if (Array.isArray(config["effectiveIngressTargets"])) {
    effectiveIngressTargets.value = config["effectiveIngressTargets"];
  } else {
    effectiveIngressTargets.value = [];
  }
}

function edgeListFromPayload(payload) {
  if (Array.isArray(payload?.edges)) {
    return payload.edges;
  }
  if (Array.isArray(payload)) {
    return payload;
  }
  return [];
}

function domainListFromPayload(payload) {
  if (Array.isArray(payload?.domains)) {
    return payload.domains;
  }
  if (Array.isArray(payload)) {
    return payload;
  }
  return [];
}

function loadConfig() {
  configLoading.value = true;
  return axios
    .get("/cockpit/api/funnel/config")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取公网入口配置失败");
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

function loadEdges() {
  return axios
    .get("/cockpit/api/funnel/edges")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取入口节点失败");
      }
      edges.value = edgeListFromPayload(payload);
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    });
}

function loadDomains() {
  domainsLoading.value = true;
  return axios
    .get("/cockpit/api/funnel/domains")
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "获取域名状态失败");
      }
      domains.value = domainListFromPayload(payload);
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      domainsLoading.value = false;
    });
}

function saveConfig() {
  saveConfigText.value = "保存中...";
  axios
    .post("/cockpit/api/funnel/config", {
      managedBaseDomain: managedBaseDomain.value,
      defaultEdgeMode: defaultEdgeMode.value,
      defaultListenerMode: defaultListenerMode.value,
      directBindAddrs: normalizeListText(directBindAddrsText.value),
      directBindPorts: normalizePortList(directBindPortsText.value),
      trustedProxyCIDRs: normalizeListText(trustedProxyCIDRsText.value),
    })
    .then(function (response) {
      const payload = unwrapData(response.data);
      if (!payload) {
        throw new Error(response.data["status"]?.substring(6) || "保存公网入口配置失败");
      }
      applyConfigPayload(payload);
      saveConfigText.value = "已保存!";
      setTimeout(function () {
        saveConfigText.value = "保存";
      }, 1500);
      toastMsg.value = "已更新公网入口平台配置";
      toastShow.value = true;
      loadEdges().then().catch();
    })
    .catch(function (error) {
      saveConfigText.value = "保存";
      toastMsg.value = String(error);
      toastShow.value = true;
    });
}

function verifyDomain(domain) {
  verifyingDomainID.value = domain?.id || domain?.stableId || "";
  axios
    .post("/cockpit/api/funnel/domains/verify", {
      id: domain?.id,
      domainId: domain?.id,
      stableId: domain?.stableId,
      domain: domain?.domain,
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

function renewCert(domain) {
  renewingDomainID.value = domain?.id || domain?.stableId || "";
  axios
    .post("/cockpit/api/funnel/certs/renew", {
      id: domain?.certId || domain?.certID || domain?.id,
      certId: domain?.certId || domain?.certID,
      domainId: domain?.id,
      stableId: domain?.stableId,
      domain: domain?.domain,
    })
    .then(function (response) {
      if (response.data["status"] != "success") {
        throw new Error(response.data["status"]?.substring(6) || "证书续期请求失败");
      }
      toastMsg.value = "已提交证书续期请求";
      toastShow.value = true;
      loadDomains().then().catch();
    })
    .catch(function (error) {
      toastMsg.value = String(error);
      toastShow.value = true;
    })
    .finally(function () {
      renewingDomainID.value = "";
    });
}

function edgeBadgeText(edge) {
  if (edge?.edgeType == "server") {
    return "Server Edge";
  }
  if (edge?.edgeType == "navi") {
    return "Remote Edge";
  }
  return edge?.edgeType || "未知";
}

function edgeCapabilities(edge) {
  const caps = edge?.listenerCapabilities || {};
  const result = [];
  if (caps["supports_http"]) result.push("HTTP");
  if (caps["supports_ws"]) result.push("WS");
  if (caps["supports_tcp"]) result.push("TCP");
  if (caps["supports_tls_terminated_tcp"]) result.push("TLS-TCP");
  if (caps["supports_proxy_protocol"]) result.push("PROXY");
  if (caps["supports_forwarded_headers"]) result.push("X-Forwarded");
  if (caps["supports_direct_bind"]) result.push("直监听");
  return result;
}

const hasIngressTargets = computed(() => {
  return effectiveIngressTargets.value.length > 0;
});

onMounted(() => {
  loadConfig().then().catch();
  loadEdges().then().catch();
  loadDomains().then().catch();
});
</script>

<template>
  <div class="flex-1">
    <div class="text-3xl font-semibold tracking-tight leading-tight mb-2 flex items-center">
      <h1 class="mr-2" tabindex="-1">公网入口</h1>
    </div>
    <div class="text-gray-600 mt-3">
      <p>配置 Mirage Funnel 的平台级域名、监听模式和入口节点清单。</p>
      <p class="text-sm text-gray-400 mt-1">
        这一批先打通控制面和配置页，不直接监听公网流量。
      </p>
    </div>

    <div class="mt-6 space-y-8">
      <section>
        <header class="max-w-2xl">
          <h3 class="text-xl font-semibold tracking-tight">平台设置</h3>
        </header>
        <p class="mt-3 text-gray-600">设置托管域名后缀、默认入口模式和监听地址。</p>
        <div class="mt-4 grid gap-5 lg:grid-cols-2">
          <div>
            <p class="text-gray-600">托管基础域名</p>
            <p class="text-sm text-gray-400">
              例如 <code class="bg-gray-200 text-xs rounded px-1">public.example.com</code>
            </p>
            <input
              v-model="managedBaseDomain"
              class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
              placeholder="public.example.com"
            />
          </div>
          <div>
            <p class="text-gray-600">默认入口节点模式</p>
            <select
              v-model="defaultEdgeMode"
              class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
            >
              <option value="server_edge">MirageServer 直出</option>
              <option value="remote_edge">Remote Edge</option>
            </select>
          </div>
          <div>
            <p class="text-gray-600">默认监听模式</p>
            <select
              v-model="defaultListenerMode"
              class="mt-2 py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md bg-white"
            >
              <option value="direct">直接监听</option>
              <option value="behind_proxy">前置代理</option>
            </select>
          </div>
          <div>
            <p class="text-gray-600">直监听地址</p>
            <p class="text-sm text-gray-400">用逗号或换行分隔，例如 0.0.0.0, ::</p>
            <textarea
              v-model="directBindAddrsText"
              rows="3"
              class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
              placeholder="0.0.0.0, ::"
            ></textarea>
          </div>
          <div>
            <p class="text-gray-600">直监听端口</p>
            <p class="text-sm text-gray-400">用逗号分隔，例如 80, 443</p>
            <input
              v-model="directBindPortsText"
              class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
              placeholder="80, 443"
            />
          </div>
          <div>
            <p class="text-gray-600">可信代理 CIDR</p>
            <p class="text-sm text-gray-400">仅在前置代理模式下信任这些来源。</p>
            <textarea
              v-model="trustedProxyCIDRsText"
              rows="3"
              class="mt-2 outline-none py-2 px-3 w-full border border-stone-200 hover:border-stone-400 rounded-md font-mono text-sm"
              placeholder="10.0.0.0/24"
            ></textarea>
          </div>
        </div>
        <div class="mt-5 flex items-center gap-3">
          <button
            @click="saveConfig"
            :disabled="configLoading"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
          >
            {{ saveConfigText }}
          </button>
          <button
            @click="loadConfig"
            class="btn h-9 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
          >
            刷新配置
          </button>
        </div>

        <div class="mt-5">
          <p class="text-gray-600">生效中的入口目标</p>
          <div v-if="hasIngressTargets" class="mt-2 flex flex-wrap gap-2">
            <span
              v-for="target in effectiveIngressTargets"
              :key="typeof target == 'string' ? target : JSON.stringify(target)"
              class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-100 text-gray-700 rounded-full px-3 py-1 leading-none text-xs"
            >
              {{ typeof target == "string" ? target : target?.label || JSON.stringify(target) }}
            </span>
          </div>
          <p v-else class="mt-2 text-sm text-gray-400">当前还没有可展示的入口目标。</p>
        </div>
      </section>

      <section>
        <header class="max-w-2xl">
          <h3 class="text-xl font-semibold tracking-tight">入口节点</h3>
        </header>
        <p class="mt-3 text-gray-600">这里展示当前已知的 server-edge / remote-edge 执行目标。</p>
        <div class="mt-4 overflow-x-auto border border-stone-200 rounded-xl">
          <table class="table w-full">
            <thead>
              <tr>
                <th>节点</th>
                <th>类型</th>
                <th>状态</th>
                <th>能力</th>
                <th>公网地址</th>
                <th>可分配</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="edge in edges" :key="edge.id || edge.stableId || edge.hostname">
                <td>
                  <div class="font-semibold text-gray-900">{{ edge.hostname || "-" }}</div>
                  <div class="text-xs text-gray-400 font-mono">{{ edge.stableId || edge.edgeNodeId || "-" }}</div>
                </td>
                <td>
                  <span
                    class="inline-flex items-center align-middle justify-center font-medium border border-blue-50 bg-blue-50 text-blue-700 rounded-sm px-2 py-1 leading-none text-xs"
                  >
                    {{ edgeBadgeText(edge) }}
                  </span>
                </td>
                <td>{{ edge.healthStatus || "unknown" }}</td>
                <td>
                  <div class="flex flex-wrap gap-1">
                    <span
                      v-for="cap in edgeCapabilities(edge)"
                      :key="cap"
                      class="inline-flex items-center align-middle justify-center font-medium border border-stone-200 bg-stone-100 text-gray-700 rounded-sm px-2 py-1 leading-none text-xs"
                    >
                      {{ cap }}
                    </span>
                  </div>
                </td>
                <td class="font-mono text-xs">
                  <div
                    v-for="addr in edge.publicAddrs || []"
                    :key="`${addr.network}-${addr.address}-${addr.port}`"
                  >
                    {{ addr.network }}://{{ addr.address }}:{{ addr.port }}
                  </div>
                </td>
                <td>{{ edge.allocatable ? "是" : "否" }}</td>
              </tr>
              <tr v-if="edges.length == 0">
                <td colspan="6" class="text-center text-sm text-gray-400 py-6">暂无入口节点信息</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <header class="max-w-2xl">
          <h3 class="text-xl font-semibold tracking-tight">域名与证书操作</h3>
        </header>
        <p class="mt-3 text-gray-600">
          第一批先保留控制面操作入口。验证和续期请求会走 deferred 流程，不会立即触发真实 DNS 或 ACME。
        </p>
        <div class="mt-4 overflow-x-auto border border-stone-200 rounded-xl">
          <table class="table w-full">
            <thead>
              <tr>
                <th>域名</th>
                <th>租户</th>
                <th>域名状态</th>
                <th>DNS</th>
                <th>证书</th>
                <th class="text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="domain in domains" :key="domain.id || domain.stableId || domain.domain">
                <td>
                  <div class="font-semibold text-gray-900">{{ domain.domain || "-" }}</div>
                  <div class="text-xs text-gray-400 font-mono">{{ domain.stableId || "-" }}</div>
                </td>
                <td>{{ domain.orgName || domain.orgID || domain.orgId || "-" }}</td>
                <td>{{ domain.status || "-" }}</td>
                <td>{{ domain.dnsStatus || domain.dns_status || "-" }}</td>
                <td>{{ domain.certStatus || domain.cert_status || "-" }}</td>
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
                      @click="renewCert(domain)"
                      :disabled="renewingDomainID == (domain.id || domain.stableId || '')"
                      class="btn h-8 min-h-fit border-stone-300 bg-white hover:bg-stone-100 text-stone-700"
                    >
                      {{ renewingDomainID == (domain.id || domain.stableId || "") ? "续期中..." : "续期" }}
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="!domainsLoading && domains.length == 0">
                <td colspan="6" class="text-center text-sm text-gray-400 py-6">当前没有可操作的 Funnel 域名记录</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>

  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>
