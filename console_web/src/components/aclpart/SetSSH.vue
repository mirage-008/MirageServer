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
const sourcesText = ref("");
const destinationsText = ref("");
const usersText = ref("");
const checkPeriod = ref("1h");
const wrongSources = ref(false);
const wrongDestinations = ref(false);
const wrongUsers = ref(false);
const wrongCheckPeriod = ref(false);

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

watch(
  () => usersText.value,
  () => {
    wrongUsers.value = false;
  }
);

watch(
  () => checkPeriod.value,
  () => {
    wrongCheckPeriod.value = false;
  }
);

watch(
  () => action.value,
  () => {
    if (action.value != "check") {
      wrongCheckPeriod.value = false;
    }
  }
);

onMounted(() => {
  if (props.rule) {
    action.value = props.rule.action || "accept";
    sourcesText.value = props.rule.src ? props.rule.src.join("\n") : "";
    destinationsText.value = props.rule.dst ? props.rule.dst.join("\n") : "";
    usersText.value = props.rule.users ? props.rule.users.join("\n") : "";
    checkPeriod.value = props.rule.checkPeriod || "1h";
  } else {
    action.value = "accept";
    sourcesText.value = "";
    destinationsText.value = "";
    usersText.value = "root\nautogroup:nonroot";
    checkPeriod.value = "1h";
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
  const users = parseRuleLines(usersText.value);

  if (sources.length == 0) {
    wrongSources.value = true;
    return;
  }
  if (destinations.length == 0) {
    wrongDestinations.value = true;
    return;
  }
  if (users.length == 0) {
    wrongUsers.value = true;
    return;
  }
  if (action.value == "check" && checkPeriod.value.trim() == "") {
    wrongCheckPeriod.value = true;
    return;
  }

  inputBlocking.value = true;
  axios
    .post("/admin/api/acls/ssh", {
      state: isEditing.value ? "update" : "create",
      id: isEditing.value ? props.rule.id : -1,
      rule: {
        action: action.value,
        src: sources,
        dst: destinations,
        users: users,
        checkPeriod: action.value == "check" ? checkPeriod.value.trim() : "",
      },
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("saved-rule");
        emit("close");
      } else if (response.data["status"] == "error-SSH检查时长无效") {
        wrongCheckPeriod.value = true;
      } else {
        toastMsg.value = "保存 SSH 规则失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存 SSH 规则失败:" + error;
      toastShow.value = true;
    })
    .then(function () {
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
          {{ isEditing ? "编辑 SSH 规则" : "创建 SSH 规则" }}
        </div>
      </header>
      <form @submit.prevent="saveRule">
        <p class="text-gray-700 mb-6">
          每行填写一个来源、目标或 SSH 用户。目标 <code>dst</code> 不带端口，按机器别名、
          用户、用户组、标签或 IP/CIDR 解析。
        </p>

        <label for="ssh-action" class="block font-medium mt-2 mb-2">动作</label>
        <select
          v-model="action"
          class="select select-bordered w-full h-9 min-h-fit"
          :disabled="inputBlocking"
          id="ssh-action"
        >
          <option value="accept">accept</option>
          <option value="check">check</option>
        </select>

        <label for="ssh-sources" class="block font-medium mt-6 mb-2">来源（src）</label>
        <textarea
          v-model="sourcesText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[8rem]"
          :disabled="inputBlocking"
          id="ssh-sources"
          placeholder="每行一个来源，例如&#10;alice&#10;group:dev&#10;tag:laptop&#10;autogroup:member"
        ></textarea>
        <p v-if="wrongSources" class="text-sm text-red-500 mt-2">请至少填写一个来源项</p>

        <label for="ssh-destinations" class="block font-medium mt-6 mb-2">目标（dst）</label>
        <textarea
          v-model="destinationsText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[8rem]"
          :disabled="inputBlocking"
          id="ssh-destinations"
          placeholder="每行一个目标，例如&#10;db-server&#10;tag:prod&#10;group:ops&#10;100.64.0.7"
        ></textarea>
        <p v-if="wrongDestinations" class="text-sm text-red-500 mt-2">请至少填写一个目标项</p>

        <label for="ssh-users" class="block font-medium mt-6 mb-2">SSH 用户（users）</label>
        <textarea
          v-model="usersText"
          class="textarea w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md min-h-[8rem]"
          :disabled="inputBlocking"
          id="ssh-users"
          placeholder="每行一个 SSH 用户，例如&#10;root&#10;ubuntu&#10;autogroup:nonroot"
        ></textarea>
        <p v-if="wrongUsers" class="text-sm text-red-500 mt-2">请至少填写一个 SSH 用户</p>

        <div v-if="action == 'check'">
          <label for="ssh-check-period" class="block font-medium mt-6 mb-2">检查时长（checkPeriod）</label>
          <input
            v-model="checkPeriod"
            class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
            type="text"
            :disabled="inputBlocking"
            id="ssh-check-period"
            placeholder="例如 1h / 30m / 24h"
          />
          <p v-if="wrongCheckPeriod" class="text-sm text-red-500 mt-2">请输入合法的检查时长</p>
        </div>

        <div class="rounded-md border border-stone-200 bg-stone-50 p-4 mt-6 text-sm text-gray-600">
          <div class="font-medium text-gray-700 mb-2">填写提示</div>
          <ul class="list-disc list-inside space-y-1">
            <li>来源和目标都支持现有 ACL 别名，例如用户、<code>group:xxx</code>、<code>tag:xxx</code>、IP 或 CIDR</li>
            <li><code>check</code> 动作需要填写 Go duration 格式的时长，例如 <code>1h</code> 或 <code>30m</code></li>
            <li><code>users</code> 是目标机器上允许登录的本地用户名列表，不是 Mirage 用户名</li>
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
