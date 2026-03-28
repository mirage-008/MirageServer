<script setup>
import { ref, watch, onMounted } from "vue";
import { useDisScroll } from "/src/utils.js";

const emit = defineEmits(["update-done", "update-fail", "close"]);

useDisScroll();

const props = defineProps({
  id: String,
  currentAddresses: {
    type: Array,
    default: () => [],
  },
});

const inputBlocking = ref(false);
const ipv4 = ref("");
const ipv6 = ref("");
const changed = ref(false);

function splitAddresses(addresses) {
  let nextIPv4 = "";
  let nextIPv6 = "";
  for (const address of addresses || []) {
    if (!address) {
      continue;
    }
    if (address.includes(":")) {
      nextIPv6 = address;
    } else {
      nextIPv4 = address;
    }
  }
  return { nextIPv4, nextIPv6 };
}

function refreshState() {
  const { nextIPv4, nextIPv6 } = splitAddresses(props.currentAddresses);
  ipv4.value = nextIPv4;
  ipv6.value = nextIPv6;
  changed.value = false;
}

watch(
  () => props.currentAddresses,
  () => {
    refreshState();
  },
  { deep: true }
);

watch([ipv4, ipv6], () => {
  const current = splitAddresses(props.currentAddresses);
  changed.value =
    ipv4.value.trim() !== current.nextIPv4.trim() || ipv6.value.trim() !== current.nextIPv6.trim();
});

onMounted(() => {
  refreshState();
});

function updateAddresses() {
  inputBlocking.value = true;
  axios
    .post("/admin/api/machines", {
      mid: props.id,
      state: "set-addresses",
      addresses: [ipv4.value.trim(), ipv6.value.trim()],
    })
    .then(function (response) {
      if (response.data["status"] == "success") {
        emit("update-done", response.data["data"]["addresses"] || []);
      } else {
        emit("update-fail", response.data["status"].substring(6));
      }
    })
    .catch(function (error) {
      emit("update-fail", error);
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
      class="bg-white rounded-lg relative p-4 md:p-6 text-gray-700 max-w-lg min-w-[19rem] my-8 mx-auto w-[97%] shadow-2xl"
      style="pointer-events: auto"
    >
      <header class="flex items-center justify-between space-x-4 mb-5 mr-8">
        <div class="font-semibold text-lg truncate">修改设备 IP</div>
      </header>
      <div class="text-gray-700 mb-6">
        只能填写当前尾网地址池内且未被占用的地址。未修改的地址保持原值。
      </div>

      <label for="machine-ipv4" class="block font-medium mt-4 mb-2">IPv4</label>
      <input
        id="machine-ipv4"
        v-model="ipv4"
        :disabled="inputBlocking"
        class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
        type="text"
        placeholder="100.64.x.x"
      />

      <label for="machine-ipv6" class="block font-medium mt-6 mb-2">IPv6</label>
      <input
        id="machine-ipv6"
        v-model="ipv6"
        :disabled="inputBlocking"
        class="input w-full border focus:outline-blue-500/60 hover:border disabled:hover:border-stone-200 disabled:border-stone-200 border-stone-200 hover:border-stone-400 rounded-md h-9 min-h-fit"
        type="text"
        placeholder="fd7a:115c:a1e0::x"
      />

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
          :disabled="inputBlocking || !changed"
          @click="updateAddresses"
          class="btn border-0 bg-blue-500 hover:bg-blue-900 disabled:bg-blue-500/60 text-white disabled:text-white/60 h-9 min-h-fit"
          type="button"
        >
          保存
        </button>
      </footer>

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
</template>
