import { ElMessageBox } from 'element-plus';

export function validFileName(name: string) {
  return Boolean(name.trim()) && name !== '.' && name !== '..' && !/[\/\x00-\x1f]/.test(name) || '名称不能为空，不能为 .、.. 或包含 / 和控制字符';
}
export function duplicateName(name: string, directory: boolean, names: Set<string>) {
  const dot = directory ? -1 : name.lastIndexOf('.');
  const stem = dot > 0 ? name.slice(0, dot) : name, extension = dot > 0 ? name.slice(dot) : '';
  let index = 1, candidate = '';
  do { candidate = `${stem} - 副本${index === 1 ? '' : ` (${index})`}${extension}`; index++; } while (names.has(candidate));
  return candidate;
}
export async function availableFileName(name: string, directory: boolean, names: Set<string>): Promise<string | undefined> {
  if (!names.has(name)) return name;
  try {
    const { value } = await ElMessageBox.prompt(`目标已存在“${name}”。输入新名称以保留两个项目，或跳过此项目。`, '同名项目', {
      inputValue: duplicateName(name, directory, names),
      inputValidator: value => validFileName(value) !== true ? validFileName(value) : names.has(value) ? '目标名称仍已存在，请使用其他名称' : true,
      confirmButtonText: '使用新名称', cancelButtonText: '跳过', distinguishCancelAndClose: true, closeOnClickModal: false,
    });
    return value;
  } catch (action) { if (action === 'cancel') return undefined; throw action; }
}
