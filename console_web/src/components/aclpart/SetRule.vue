<script setup>
import { watch, ref, computed, onMounted } from "vue";
import Toast from "../Toast.vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["saved-rule", "close"]);

const props = defineProps({
  rule: Object,
});

useDisScroll();

const toastShow = ref(false);
const toastMsg = ref("");
watch(toastShow, () => {
  if (toastShow.value) {
    setTimeout(function () {
      toastShow.value = false;
    }, 5000);
  }
});

const inputBlocking = ref(false);
const aliasLoading = ref(false);
const action = ref("accept");
const protocol = ref("");
const sourceItems = ref([""]);
const destinationRows = ref([createDestinationRow()]);
const wrongSources = ref(false);
const wrongDestinations = ref(false);

const availableUsers = ref([]);
const availableGroups = ref([]);
const availableTags = ref([]);
const availableHosts = ref([]);

const isEditing = computed(() => {
  return props.rule != null;
});

const normalizedSources = computed(() => {
  return normalizeRuleItems(sourceItems.value);
});

const normalizedDestinations = computed(() => {
  return normalizeDestinationRows(destinationRows.value);
});

const sourceSuggestions = computed(() => {
  return uniqueSortedList([
    "*",
    "autogroup:member",
    "autogroup:tagged",
    ...availableUsers.value,
    ...availableGroups.value,
    ...availableTags.value,
  ]);
});

const destinationTargetSuggestions = computed(() => {
  return uniqueSortedList([
    "*",
    "autogroup:self",
    "autogroup:internet",
    ...availableHosts.value,
    ...availableTags.value,
  ]);
});

const destinationPortSuggestions = ["*", "443", "80", "53", "22", "3389"];

const destinationRowIssues = computed(() => {
  return destinationRows.value.map(function (row) {
    const target = normalizeRuleItem(row.target);
    const ports = normalizeRuleItem(row.ports);
    return {
      missingTarget: target == "" && ports != "",
      missingPorts: target != "" && ports == "",
    };
  });
});

const previewText = computed(() => {
  const payload = {
    action: action.value,
    src: normalizedSources.value,
    dst: normalizedDestinations.value,
  };
  if (protocol.value.trim() != "") {
    payload.proto = protocol.value.trim();
  }
  return JSON.stringify(payload, null, 2);
});

watch(
  () => sourceItems.value,
  () => {
    wrongSources.value = false;
  },
  { deep: true }
);

watch(
  () => destinationRows.value,
  () => {
    wrongDestinations.value = false;
  },
  { deep: true }
);

onMounted(() => {
  if (props.rule) {
    action.value = props.rule.action || "accept";
    protocol.value = props.rule.proto || "";
    sourceItems.value = props.rule.src && props.rule.src.length > 0 ? props.rule.src.slice() : [""];
    destinationRows.value =
      props.rule.dst && props.rule.dst.length > 0
        ? props.rule.dst.map(function (item) {
            return parseDestinationItem(item);
          })
        : [createDestinationRow()];
  } else {
    action.value = "accept";
    protocol.value = "";
    sourceItems.value = [""];
    destinationRows.value = [createDestinationRow()];
  }

  loadAliasCatalog();
});

function uniqueSortedList(items) {
  return Array.from(
    new Set(
      items
        .map(function (item) {
          return String(item || "").trim();
        })
        .filter(function (item) {
          return item != "";
        })
    )
  ).sort(function (a, b) {
    return a.localeCompare(b);
  });
}

function normalizeRuleItems(items) {
  return items
    .map(function (item) {
      return normalizeRuleItem(item);
    })
    .filter(function (item) {
      return item != "";
    });
}

function normalizeRuleItem(item) {
  return String(item || "").trim();
}

function createDestinationRow(target = "", ports = "") {
  return {
    target: normalizeRuleItem(target),
    ports: normalizeRuleItem(ports),
  };
}

function parseDestinationItem(item) {
  item = normalizeRuleItem(item);
  if (item == "") {
    return createDestinationRow();
  }

  const separator = item.lastIndexOf(":");
  if (separator == -1) {
    return createDestinationRow(item, "");
  }

  return createDestinationRow(item.slice(0, separator), item.slice(separator + 1));
}

function normalizeDestinationRows(rows) {
  return rows
    .map(function (row) {
      return createDestinationRow(row.target, row.ports);
    })
    .filter(function (row) {
      return row.target != "" || row.ports != "";
    })
    .filter(function (row) {
      return row.target != "" && row.ports != "";
    })
    .map(function (row) {
      return row.target + ":" + row.ports;
    });
}

function ensureOneEditableRow(items) {
  return items.length > 0 ? items : [""];
}

function ensureOneDestinationRow(rows) {
  return rows.length > 0 ? rows : [createDestinationRow()];
}

function addSourceRow(value = "") {
  sourceItems.value = sourceItems.value.concat(value);
}

function addDestinationRow(target = "", ports = "") {
  destinationRows.value = destinationRows.value.concat(createDestinationRow(target, ports));
}

function removeSourceRow(index) {
  sourceItems.value = ensureOneEditableRow(
    sourceItems.value.filter(function (_, currentIndex) {
      return currentIndex != index;
    })
  );
}

function removeDestinationRow(index) {
  destinationRows.value = ensureOneDestinationRow(
    destinationRows.value.filter(function (_, currentIndex) {
      return currentIndex != index;
    })
  );
}

function addUniqueSource(value) {
  value = String(value || "").trim();
  if (value == "") {
    return;
  }
  if (normalizedSources.value.includes(value)) {
    return;
  }
  if (sourceItems.value.length == 1 && sourceItems.value[0].trim() == "") {
    sourceItems.value = [value];
    return;
  }
  addSourceRow(value);
}

function addSuggestedDestinationTarget(value) {
  value = String(value || "").trim();
  if (value == "") {
    return;
  }

  const blankRowIndex = destinationRows.value.findIndex(function (row) {
    return normalizeRuleItem(row.target) == "" && normalizeRuleItem(row.ports) == "";
  });
  if (blankRowIndex != -1) {
    destinationRows.value[blankRowIndex].target = value;
    destinationRows.value[blankRowIndex].ports = "*";
    return;
  }
  addDestinationRow(value, "*");
}

function useProtocolPreset(value) {
  protocol.value = value;
}

function useDestinationPortPreset(index, value) {
  destinationRows.value[index].ports = value;
}

function loadAliasCatalog() {
  aliasLoading.value = true;
  Promise.allSettled([
    axios.get("/admin/api/users"),
    axios.get("/admin/api/acls/groups"),
    axios.get("/admin/api/acls/tags"),
    axios.get("/admin/api/acls/hosts"),
  ])
    .then(function (results) {
      const usersResponse = results[0];
      const groupsResponse = results[1];
      const tagsResponse = results[2];
      const hostsResponse = results[3];

      if (
        usersResponse.status == "fulfilled" &&
        usersResponse.value.data["status"] == "success"
      ) {
        availableUsers.value =
          usersResponse.value.data["data"]["users"]?.map(function (user) {
            return user.loginName;
          }) || [];
      }
      if (
        groupsResponse.status == "fulfilled" &&
        groupsResponse.value.data["status"] == "success"
      ) {
        availableGroups.value =
          groupsResponse.value.data["data"]["groups"]?.map(function (group) {
            return group.groupName;
          }) || [];
      }
      if (
        tagsResponse.status == "fulfilled" &&
        tagsResponse.value.data["status"] == "success"
      ) {
        availableTags.value =
          tagsResponse.value.data["data"]["tagOwners"]?.map(function (tag) {
            return tag.tagName;
          }) || [];
      }
      if (
        hostsResponse.status == "fulfilled" &&
        hostsResponse.value.data["status"] == "success"
      ) {
        availableHosts.value =
          hostsResponse.value.data["data"]["hosts"]?.map(function (host) {
            return host.hostName;
          }) || [];
      }
    })
    .finally(function () {
      aliasLoading.value = false;
    });
}

function saveRule() {
  const sources = normalizedSources.value;
  const destinations = normalizedDestinations.value;

  if (sources.length == 0) {
    wrongSources.value = true;
    return;
  }
  if (destinations.length == 0) {
    wrongDestinations.value = true;
    return;
  }
  if (
    destinationRowIssues.value.some(function (issue) {
      return issue.missingTarget || issue.missingPorts;
    })
  ) {
    wrongDestinations.value = true;
    return;
  }

  inputBlocking.value = true;
  axios
    .post("/admin/api/acls/rules", {
      state: isEditing.value ? "update" : "create",
      id: isEditing.value ? props.rule.id : -1,
      rule: {
        action: action.value,
        proto: protocol.value.trim(),
        src: sources,
        dst: destinations,
      },
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("saved-rule");
        emit("close");
      } else {
        toastMsg.value = "保存 ACL 规则失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存 ACL 规则失败:" + error;
      toastShow.value = true;
    })
    .finally(function () {
      inputBlocking.value = false;
    });
}
</script>

<template>
  <div
    @click.self="$emit('close')"
    class="fixed overflow-y-auto inset-0 py-8 z-30 bg-gray-900 bg-opacity-[0.07]"
    style="pointer-events: auto"
  >
    <div
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-5xl min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">
          {{ isEditing ? "编辑 ACL 规则" : "创建 ACL 规则" }}
        </div>
      </header>
      <form @submit.prevent="saveRule">
        <div class="rounded-md border border-blue-100 bg-blue-50 px-4 py-3 text-sm text-blue-900">
          这是一版面向规则条目的 visual editor：你可以逐项添加来源和目标，不必手写多行 JSON。
          如需直接编辑整份策略，请切到左侧的 <code>JSON 策略</code> 页面。
        </div>

        <div class="grid gap-4 md:grid-cols-[12rem_minmax(0,1fr)] mt-6">
          <div>
            <label for="rule-action" class="block font-medium mt-2 mb-2">动作</label>
            <div
              id="rule-action"
              class="flex items-center w-full h-10 rounded-md border border-stone-200 bg-stone-50 px-3 text-sm text-gray-600"
            >
              <code>accept</code>
            </div>
            <p class="text-xs text-gray-500 mt-2">
              当前 ACL 规则仅支持 <code>accept</code>；会话审批请改用 SSH 规则里的
              <code>check</code>。
            </p>
          </div>

          <div>
            <label for="rule-protocol" class="block font-medium mt-2 mb-2">协议</label>
            <input
              v-model="protocol"
              class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-10 min-h-fit"
              type="text"
              :disabled="inputBlocking"
              id="rule-protocol"
              placeholder="留空表示默认；也可填 tcp / udp / icmp"
            />
            <div class="flex flex-wrap gap-2 mt-3">
              <button
                v-for="item in [
                  { label: '默认', value: '' },
                  { label: 'tcp', value: 'tcp' },
                  { label: 'udp', value: 'udp' },
                  { label: 'icmp', value: 'icmp' },
                ]"
                :key="item.label"
                type="button"
                :disabled="inputBlocking"
                @click="useProtocolPreset(item.value)"
                class="inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium transition"
                :class="
                  protocol.trim() == item.value
                    ? 'border-blue-200 bg-blue-100 text-blue-900'
                    : 'border-stone-200 bg-stone-50 text-stone-600 hover:border-stone-300'
                "
              >
                {{ item.label }}
              </button>
            </div>
          </div>
        </div>

        <div class="grid gap-6 lg:grid-cols-2 mt-8">
          <section class="rounded-xl border border-stone-200 bg-white overflow-hidden">
            <header class="border-b border-stone-200 px-4 py-4">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <div class="font-semibold text-lg">来源（src）</div>
                  <p class="text-sm text-gray-500 mt-1">
                    可选用户、<code>group:xxx</code>、<code>tag:xxx</code>、<code>*</code> 等别名。
                  </p>
                </div>
                <button
                  type="button"
                  :disabled="inputBlocking"
                  @click="addSourceRow('')"
                  class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit font-normal"
                >
                  添加来源
                </button>
              </div>
            </header>
            <div class="p-4">
              <div class="space-y-3">
                <div
                  v-for="(item, index) in sourceItems"
                  :key="'src-' + index"
                  class="flex items-center gap-3"
                >
                  <div class="w-8 shrink-0 text-right text-xs text-gray-400">
                    {{ index + 1 }}
                  </div>
                  <input
                    v-model="sourceItems[index]"
                    list="acl-rule-source-suggestions"
                    class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-10 min-h-fit"
                    :disabled="inputBlocking"
                    type="text"
                    placeholder="例如 alice / group:dev / tag:laptop / *"
                  />
                  <button
                    type="button"
                    :disabled="inputBlocking"
                    @click="removeSourceRow(index)"
                    class="btn btn-sm btn-ghost shrink-0 border-0 bg-base-0 hover:bg-base-200"
                  >
                    删除
                  </button>
                </div>
              </div>
              <p v-if="wrongSources" class="text-sm text-red-500 mt-3">请至少填写一个来源项</p>

              <div class="mt-5">
                <div class="text-xs uppercase tracking-wide text-gray-400 mb-2">快速添加</div>
                <div v-if="aliasLoading" class="text-sm text-gray-500">正在加载可用别名…</div>
                <div v-else class="flex flex-wrap gap-2">
                  <button
                    v-for="item in sourceSuggestions.slice(0, 18)"
                    :key="'source-chip-' + item"
                    type="button"
                    :disabled="inputBlocking"
                    @click="addUniqueSource(item)"
                    class="inline-flex items-center rounded-full border border-stone-200 bg-stone-50 px-3 py-1 text-xs font-medium text-stone-600 transition hover:border-stone-300 hover:bg-stone-100"
                  >
                    {{ item }}
                  </button>
                </div>
              </div>
            </div>
          </section>

          <section class="rounded-xl border border-stone-200 bg-white overflow-hidden">
            <header class="border-b border-stone-200 px-4 py-4">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <div class="font-semibold text-lg">目标（dst）</div>
                  <p class="text-sm text-gray-500 mt-1">
                    每行拆成“目标选择器 + 端口”两列，更接近官方 visual editor 的填写方式。
                  </p>
                </div>
                <button
                  type="button"
                  :disabled="inputBlocking"
                  @click="addDestinationRow('')"
                  class="btn border border-stone-300 hover:border-stone-300 disabled:border-stone-300 bg-base-200 hover:bg-base-300 disabled:bg-base-200/60 text-black disabled:text-black/30 h-9 min-h-fit font-normal"
                >
                  添加目标
                </button>
              </div>
            </header>
            <div class="p-4">
              <div class="space-y-3">
                <div
                  v-for="(row, index) in destinationRows"
                  :key="'dst-' + index"
                  class="rounded-lg border border-stone-200 bg-stone-50 p-3"
                >
                  <div class="grid gap-3 lg:grid-cols-[2rem_minmax(0,1fr)_9rem_auto] items-start">
                    <div class="pt-3 text-right text-xs text-gray-400">
                      {{ index + 1 }}
                    </div>
                    <div>
                      <label class="block text-xs font-medium text-gray-500 mb-2">目标选择器</label>
                      <input
                        v-model="destinationRows[index].target"
                        list="acl-rule-destination-target-suggestions"
                        class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 rounded-md h-10 min-h-fit"
                        :class="
                          destinationRowIssues[index]?.missingTarget
                            ? 'border-red-400 hover:border-red-400 focus:outline-red-500/60'
                            : 'border-stone-200 hover:border-stone-400'
                        "
                        :disabled="inputBlocking"
                        type="text"
                        placeholder="例如 tag:web / internal-db / autogroup:self / *"
                      />
                    </div>
                    <div>
                      <label class="block text-xs font-medium text-gray-500 mb-2">端口</label>
                      <input
                        v-model="destinationRows[index].ports"
                        class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 rounded-md h-10 min-h-fit"
                        :class="
                          destinationRowIssues[index]?.missingPorts
                            ? 'border-red-400 hover:border-red-400 focus:outline-red-500/60'
                            : 'border-stone-200 hover:border-stone-400'
                        "
                        :disabled="inputBlocking"
                        type="text"
                        placeholder="例如 * / 443 / 80,443"
                      />
                    </div>
                    <div class="pt-7">
                      <button
                        type="button"
                        :disabled="inputBlocking"
                        @click="removeDestinationRow(index)"
                        class="btn btn-sm btn-ghost shrink-0 border-0 bg-base-0 hover:bg-base-200"
                      >
                        删除
                      </button>
                    </div>
                  </div>

                  <div class="lg:ml-11 mt-3">
                    <div class="text-xs uppercase tracking-wide text-gray-400 mb-2">常用端口</div>
                    <div class="flex flex-wrap gap-2">
                      <button
                        v-for="port in destinationPortSuggestions"
                        :key="'port-chip-' + index + '-' + port"
                        type="button"
                        :disabled="inputBlocking"
                        @click="useDestinationPortPreset(index, port)"
                        class="inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium transition"
                        :class="
                          destinationRows[index].ports.trim() == port
                            ? 'border-blue-200 bg-blue-100 text-blue-900'
                            : 'border-stone-200 bg-white text-stone-600 hover:border-stone-300'
                        "
                      >
                        {{ port }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
              <p v-if="wrongDestinations" class="text-sm text-red-500 mt-3">
                请至少填写一个完整目标项；已填写目标时必须同时填写端口。
              </p>

              <div class="mt-5">
                <div class="text-xs uppercase tracking-wide text-gray-400 mb-2">快速添加目标</div>
                <div v-if="aliasLoading" class="text-sm text-gray-500">正在加载可用别名…</div>
                <div v-else class="flex flex-wrap gap-2">
                  <button
                    v-for="item in destinationTargetSuggestions.slice(0, 18)"
                    :key="'destination-chip-' + item"
                    type="button"
                    :disabled="inputBlocking"
                    @click="addSuggestedDestinationTarget(item)"
                    class="inline-flex items-center rounded-full border border-stone-200 bg-stone-50 px-3 py-1 text-xs font-medium text-stone-600 transition hover:border-stone-300 hover:bg-stone-100"
                  >
                    {{ item }}<span class="ml-1 text-stone-400">:*</span>
                  </button>
                </div>
                <p class="text-xs text-gray-500 mt-3">
                  从这里快速添加时，默认端口会带上 <code>*</code>，你可以再按行改成具体端口或端口段。
                </p>
              </div>
            </div>
          </section>
        </div>

        <div class="rounded-xl border border-stone-200 bg-stone-50 p-4 mt-6">
          <div class="font-medium text-gray-700 mb-2">实时预览</div>
          <p class="text-sm text-gray-500 mb-3">
            保存前会把当前可视化编辑结果组装成规则对象，再交给后端按现有 ACL 语义验证。
          </p>
          <pre class="bg-white border border-stone-200 rounded-lg p-4 text-xs overflow-auto"><code>{{ previewText }}</code></pre>
        </div>

        <div class="rounded-md border border-stone-200 bg-stone-50 p-4 mt-6 text-sm text-gray-600">
          <div class="font-medium text-gray-700 mb-2">填写提示</div>
          <ul class="list-disc list-inside space-y-1">
            <li>来源支持用户、<code>group:xxx</code>、<code>tag:xxx</code>、<code>*</code> 等现有 ACL 别名</li>
            <li>目标会被组装成 <code>目标:端口</code>；端口支持 <code>*</code>、单端口、逗号列表和端口段</li>
            <li><code>autogroup:self</code> 目标只允许来源为用户、用户组、<code>*</code> 或 <code>autogroup:member</code></li>
          </ul>
        </div>

        <footer class="flex mt-10 justify-end space-x-4">
          <button
            :disabled="inputBlocking"
            @click="$emit('close')"
            class="btn border border-base-300 hover:border-base-300 bg-base-200 hover:bg-base-300 text-black h-9 min-h-fit"
            type="button"
          >
            取消
          </button>
          <button
            :disabled="inputBlocking"
            class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
            type="submit"
          >
            保存
          </button>
        </footer>
      </form>

      <datalist id="acl-rule-source-suggestions">
        <option v-for="item in sourceSuggestions" :key="'src-opt-' + item" :value="item"></option>
      </datalist>
      <datalist id="acl-rule-destination-target-suggestions">
        <option
          v-for="item in destinationTargetSuggestions"
          :key="'dst-opt-' + item"
          :value="item"
        ></option>
      </datalist>

      <button
        @click="$emit('close')"
        class="btn btn-sm btn-ghost absolute top-5 right-5 px-2 py-2 border-0 bg-base-0 focus:bg-base-200 hover:bg-base-200"
        type="button"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="1.25em"
          height="1.25em"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <line x1="18" y1="6" x2="6" y2="18"></line>
          <line x1="6" y1="6" x2="18" y2="18"></line>
        </svg>
      </button>
    </div>
  </div>

  <Teleport to=".toast-container">
    <Toast :show="toastShow" :msg="toastMsg" @close="toastShow = false"></Toast>
  </Teleport>
</template>

<style scoped></style>
