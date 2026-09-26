export const questionMeta: Record<
  string,
  { name: string; description: string }
> = {
  cyber_abuse: {
    name: "网络攻击",
    description:
      "请求未经授权的入侵、恶意软件或攻击操作帮助；防御和授权测试不算。",
  },
  illicit: {
    name: "违法行为",
    description: "请求实施非计算机类违法行为的可操作步骤；法律解释不算。",
  },
  violence: {
    name: "人身伤害",
    description: "请求伤害人或动物，或制造武器、有害装置的操作帮助。",
  },
  child_safety: {
    name: "未成年人安全",
    description: "涉及未成年人的性化、诱骗、剥削或虐待。",
  },
  hate_harassment: {
    name: "仇恨与骚扰",
    description: "因身份特征攻击、贬损或威胁个人或群体。",
  },
  privacy_pii: {
    name: "隐私信息",
    description: "请求获取、暴露或推断特定个人的私密身份信息。",
  },
  fraud_deception: {
    name: "欺诈与欺骗",
    description: "请求实施欺诈、冒充或有针对性的欺骗。",
  },
  self_harm: {
    name: "自伤风险",
    description: "请求自伤或自杀的实施方法与具体指导。",
  },
  bypass_attempt: {
    name: "绕过限制",
    description: "请求绕过模型或平台的安全限制。",
  },
  sexual: {
    name: "性内容程度",
    description: "0 非露骨；1 暗示；2 露骨性内容；3 以色情为目的。",
  },
  gore: {
    name: "血腥程度",
    description:
      "0 无描写；1 情节需要；2 刻意描写血腥细节；3 只为呈现极端暴力。",
  },
};

export function questionName(key: string) {
  return questionMeta[key]?.name || key;
}
