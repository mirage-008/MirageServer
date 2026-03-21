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
const action = ref("accept");
const protocol = ref("");
const sourcesText = ref("");
const destinationsText = ref("");
const wrongSources = ref(false);
const wrongDestinations = ref(false);

const isEditing = computed(() => {
  return props.rule != null;
});

watch(
  () => sourcesText.value,
  () => {
    wrongSources.value = false;
  }
);

watch(
  () => destinationsText.value,
  () => {
    wrongDestinations.value = false;
  }
);

onMounted(() => {
  if (props.rule) {
    action.value = props.rule.action || "accept";
    protocol.value = props.rule.proto || "";
    sourcesText.value = props.rule.src ? props.rule.src.join("\n") : "";
    destinationsText.value = props.rule.dst ? props.rule.dst.join("\n") : "";
  } else {
    action.value = "accept";
    protocol.value = "";
    sourcesText.value = "";
    destinationsText.value = "";
  }
});

function parseRuleLines(text) {
  return text
    .split("\n")
    .map(function (line) {
      return line.trim();
    })
    .filter(function (line) {
      return line != "";
    });
}

function saveRule() {
  const sources = parseRuleLines(sourcesText.value);
  const destinations = parseRuleLines(destinationsText.value);

  if (sources.length == 0) {
    wrongSources.value = true;
    return;
  }
  if (destinations.length == 0) {
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
        proto: protocol.value,
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
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-2xl min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">
          {{ isEditing ? "编辑 ACL 规则" : "创建 ACL 规则" }}
        </div>
      </header>
      <form @submit.prevent="saveRule">
        <p class="text-gray-700 mb-6">
          每行填写一个来源或目标项。目标项必须带端口，例如 <code>tag:web:443</code>、
          <code>internal-db:5432</code>、<code>autogroup:self:*</code>。
        </p>

        <div class="grid md:grid-cols-2 gap-4">
          <div>
            <label for="rule-action" class="block font-medium mt-2 mb-2">动作</label>
            <div
              id="rule-action"
              class="flex items-center w-full h-9 min-h-fit rounded-md border border-stone-200 bg-stone-50 px-3 text-sm text-gray-600"
            >
              <code>accept</code>
            </div>
            <p class="text-xs text-gray-500 mt-2">
              当前 ACL 规则仅支持 <code>accept</code>；如需会话审批请使用 SSH 规则中的
              <code>check</code>。
            </p>
          </div>
          <div>
            <label for="rule-protocol" class="block font-medium mt-2 mb-2">协议</label>
            <input
              v-model="protocol"
              class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
              type="text"
              :disabled="inputBlocking"
              id="rule-protocol"
              placeholder="留空表示默认；也可填 tcp / udp / icmp"
            />
          </div>
        </div>

        <label for="rule-sources" class="block font-medium mt-6 mb-2">来源（src）</label>
        <textarea
          v-model="sourcesText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[10rem]"
          :disabled="inputBlocking"
          id="rule-sources"
          placeholder="每行一个来源，例如&#10;alice&#10;group:dev&#10;tag:client&#10;*"
        ></textarea>
        <p v-if="wrongSources" class="text-sm text-red-500 mt-2">请至少填写一个来源项</p>

        <label for="rule-destinations" class="block font-medium mt-6 mb-2">目标（dst）</label>
        <textarea
          v-model="destinationsText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[10rem]"
          :disabled="inputBlocking"
          id="rule-destinations"
          placeholder="每行一个目标，例如&#10;tag:web:443&#10;internal-db:5432&#10;autogroup:self:*&#10;autogroup:internet:*"
        ></textarea>
        <p v-if="wrongDestinations" class="text-sm text-red-500 mt-2">请至少填写一个目标项</p>

        <div class="rounded-md border border-stone-200 bg-stone-50 p-4 mt-6 text-sm text-gray-600">
          <div class="font-medium text-gray-700 mb-2">填写提示</div>
          <ul class="list-disc list-inside space-y-1">
            <li>来源支持用户、<code>group:xxx</code>、<code>tag:xxx</code>、<code>*</code> 等现有 ACL 别名</li>
            <li>目标必须包含端口；协议需要全端口时请使用 <code>*</code></li>
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
