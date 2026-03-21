<script setup>
import { ref, computed } from "vue";
import { useDisScroll } from "../utils.js";

useDisScroll();

const machineMenu = ref(null);
const props = defineProps({
  toleft: Number,
  totop: Number,
  neverExpires: Boolean,
  isExternal: Boolean,
});
const menuLeft = computed(() => {
  const menuWidth = machineMenu.value?.clientWidth || 224;
  const rawLeft = props.toleft + 32 - menuWidth;
  const maxLeft = Math.max(window.innerWidth - menuWidth - 12, 12);
  return String(Math.min(Math.max(rawLeft, 12), maxLeft));
});
const menuTop = computed(() => {
  const menuHeight = machineMenu.value?.clientHeight || 280;
  const rawTop =
    props.totop <= window.innerHeight / 2 ? props.totop + 36 : props.totop - 10 - menuHeight;
  const maxTop = Math.max(window.innerHeight - menuHeight - 12, 12);
  return String(Math.min(Math.max(rawTop, 12), maxTop));
});

const emit = defineEmits(["close"]);
const closeMe = (event) => {
  emit("close");
};

function emitIfEditable(eventName) {
  if (!props.isExternal) {
    emit(eventName);
  }
}
</script>

<template>
  <div
    ref="machineMenu"
    v-click-away="closeMe"
    class="shadow-xl border border-base-300 rounded-md z-20"
    :style="
      'position: fixed; left: ' +
      menuLeft +
      'px; top: ' +
      menuTop +
      'px; width: min(16rem, calc(100vw - 24px)); --radix-popper-transform-origin: 0% 0px;'
    "
  >
    <div
      class="dropdown bg-white rounded-md py-1 z-20 overflow-hidden"
      style="
        outline: none;
        --radix-dropdown-menu-content-transform-origin: var(
          --radix-popper-transform-origin
        );
        pointer-events: auto;
      "
    >
      <div
        @click="emitIfEditable('showdialog-updatehostname')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        编辑设备名称…
      </div>
      <div
        @click="emitIfEditable('showdialog-share')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        分享…
      </div>
      <div
        @click="emitIfEditable('set-expires')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        {{ neverExpires ? "启用密钥过期" : "禁用密钥过期" }}
      </div>
      <div class="my-1 border-b border-base-300"></div>
      <div
        @click="emitIfEditable('showdialog-setsubnet')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        编辑子网转发…
      </div>
      <div
        @click="emitIfEditable('showdialog-edittags')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        编辑ACL标签…
      </div>
      <div class="my-1 border-b border-base-300"></div>
      <div
        @click="emitIfEditable('showdialog-remove')"
        :class="{
          'cursor-pointer hover:bg-gray-100 focus:bg-gray-100 text-red-400': !isExternal,
          'cursor-default text-gray-300': isExternal,
        }"
        class="block px-4 py-2 focus:outline-none"
      >
        移除…
      </div>
      <div v-if="isExternal" class="px-4 py-2 text-xs text-gray-500 border-t border-base-300">
        外部共享设备仅支持查看，不能修改。
      </div>
    </div>
  </div>
</template>

<style scoped></style>
