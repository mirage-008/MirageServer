<script setup>
import { watch, ref, computed, onMounted } from "vue";
import Toast from "../Toast.vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["saved-host", "close"]);

const props = defineProps({
  host: Object,
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
const hostName = ref("");
const ipPrefix = ref("");
const hostNameOccupied = ref(false);
const wrongHostName = ref(false);
const wrongIPPrefix = ref(false);

const isEditing = computed(() => {
  return props.host != null;
});

watch(
  () => hostName.value,
  () => {
    hostNameOccupied.value = false;
    wrongHostName.value = false;
    hostName.value = hostName.value
      .toLowerCase()
      .replace(/[^-0-9a-z]/gi, "-")
      .replace(/--*/g, "-");
  }
);

watch(
  () => ipPrefix.value,
  () => {
    wrongIPPrefix.value = false;
  }
);

onMounted(() => {
  if (props.host) {
    hostName.value = props.host.hostName;
    ipPrefix.value = props.host.ipPrefix;
  } else {
    hostName.value = "";
    ipPrefix.value = "";
  }
});

function saveHost() {
  if (!/^([0-9a-z]|-(?!-))+$/.test(hostName.value)) {
    wrongHostName.value = true;
    return;
  }
  if (/^(-.*|.*-)$/.test(hostName.value)) {
    wrongHostName.value = true;
    return;
  }
  if (ipPrefix.value.trim() == "") {
    wrongIPPrefix.value = true;
    return;
  }

  inputBlocking.value = true;
  axios
    .post("/admin/api/acls/hosts", {
      state: isEditing.value ? "update" : "create",
      hostName: hostName.value,
      ipPrefix: ipPrefix.value,
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("saved-host");
        emit("close");
      } else if (response.data["status"] == "error-occupied") {
        hostNameOccupied.value = true;
      } else if (response.data["status"] == "error-IP或网段无效") {
        wrongIPPrefix.value = true;
      } else {
        toastMsg.value = "保存主机别名失败:" + response.data["status"].substring(6);
        toastShow.value = true;
      }
    })
    .catch(function (error) {
      toastMsg.value = "保存主机别名失败:" + error;
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
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">
          {{ isEditing ? "编辑主机别名" : "创建主机别名" }}
        </div>
      </header>
      <form @submit.prevent="saveHost">
        <p class="text-gray-700 mb-6">
          主机别名会映射到一个 IP 或网段，可在 ACL 规则的目标端复用。
        </p>

        <label for="host-name" class="block font-medium mt-6 mb-2">别名</label>
        <div class="flex mb-2">
          <div class="relative w-full z-30">
            <input
              v-model="hostName"
              class="input w-full z-30 border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
              type="text"
              :disabled="inputBlocking || isEditing"
              id="host-name"
            />
          </div>
        </div>
        <p v-if="hostNameOccupied" class="text-sm text-red-500 mb-2">
          主机别名 “{{ hostName }}” 已存在
        </p>
        <p v-if="wrongHostName" class="text-sm text-red-500 mb-2">
          主机别名不能为空，只能是字母数字和连接线组成，且不能以连接线开头结尾
        </p>

        <label for="host-prefix" class="block font-medium mt-6 mb-2">IP / 网段</label>
        <div class="flex mb-2">
          <div class="relative w-full z-30">
            <input
              v-model="ipPrefix"
              class="input w-full z-30 border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
              type="text"
              :disabled="inputBlocking"
              id="host-prefix"
              placeholder="例如 100.64.0.10 或 10.0.0.0/24"
            />
          </div>
        </div>
        <p v-if="wrongIPPrefix" class="text-sm text-red-500 mb-2">请输入合法的 IP 地址或 CIDR 网段</p>

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
            @click="saveHost"
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
