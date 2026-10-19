const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

export class CardStack {
  constructor({ flip, grade, isBusy }) {
    this.card = document.querySelector("#card");
    this.motion = document.querySelector("#motion-card");
    this.stack = document.querySelector("#stack");
    this.isBusy = isBusy;
    this.grade = grade;
    this.flipped = false;
    this.drag = null;
    this.suppressUntil = 0;
    this.card.addEventListener("click", () => {
      if (!isBusy() && performance.now() > this.suppressUntil) flip();
    });
    this.card.addEventListener("pointerdown", event => this.down(event));
    this.card.addEventListener("pointermove", event => this.move(event));
    this.card.addEventListener("pointerup", event => this.up(event));
    this.card.addEventListener("pointercancel", () => this.cancel());
    this.card.addEventListener("lostpointercapture", () => this.cancel());
  }

  render(card, remaining) {
    this.cancel();
    this.card.classList.add("no-flip");
    this.setFlipped(false);
    this.stack.classList.toggle("single", remaining === 1);
    const front = document.querySelector("#front-text");
    const back = document.querySelector("#back-main");
    const prompt = document.querySelector("#back-prompt");
    const secondary = document.querySelector("#back-secondary");
    document.querySelectorAll(".direction-badge").forEach(badge => {
      badge.textContent = card.direction.toUpperCase();
    });
    const chinese = card.direction !== "write";
    front.textContent = card.direction === "read" ? card.simplified :
      card.direction === "write" ? card.english : card.pinyin;
    front.className = `card-main${card.direction === "read" ? " chinese" : ""}`;
    front.lang = card.direction === "read" ? "zh-Hans" : card.direction === "pinyin" ? "zh-Latn" : "en";
    prompt.textContent = front.textContent;
    prompt.lang = front.lang;
    back.textContent = card.direction === "read" ? card.pinyin : card.simplified;
    back.className = `card-main${card.direction !== "read" ? " chinese" : ""}`;
    back.lang = card.direction === "read" ? "zh-Latn" : "zh-Hans";
    secondary.textContent = chinese ? card.english : card.pinyin;
    secondary.lang = chinese ? "en" : "zh-Latn";
    document.querySelector("#front-hint").textContent = {
      read: "Recall the pronunciation and meaning",
      write: "Try writing the Chinese word before flipping",
      pinyin: "Recall the character and meaning",
    }[card.direction];
    [front, back].forEach(element => {
      element.classList.toggle("long", element.textContent.length >
        (element.classList.contains("chinese") ? 4 : 30));
    });
    this.setFlipped(false);
    this.motion.style.transform = "";
    this.motion.style.opacity = "";
    this.motion.style.zIndex = "";
    // Commit the unflipped face before restoring flip transitions.
    void this.card.offsetWidth;
    this.card.classList.remove("no-flip");
  }

  setFlipped(flipped) {
    this.flipped = flipped;
    this.card.classList.toggle("flipped", flipped);
    this.card.setAttribute("aria-pressed", String(flipped));
    const content = flipped ?
      [document.querySelector("#back-prompt").textContent,
        document.querySelector("#back-main").textContent,
        document.querySelector("#back-secondary").textContent].join(". ") :
      document.querySelector("#front-text").textContent;
    this.card.setAttribute("aria-label", `${content}. ${flipped ?
      "Flip card to return to the prompt" : "Flip card to reveal answer"}`);
    const front = document.querySelector("#front-face");
    const back = document.querySelector("#back-face");
    front.setAttribute("aria-hidden", String(flipped));
    front.inert = flipped;
    back.setAttribute("aria-hidden", String(!flipped));
    back.inert = !flipped;
  }

  down(event) {
    if (this.isBusy() || !event.isPrimary || event.button !== 0) return;
    this.drag = { id: event.pointerId, x: event.clientX, y: event.clientY, dx: 0, moved: false };
    this.card.setPointerCapture(event.pointerId);
  }

  move(event) {
    if (!this.drag || this.drag.id !== event.pointerId) return;
    const dx = event.clientX - this.drag.x;
    const dy = event.clientY - this.drag.y;
    if (!this.drag.moved && Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > 12) {
      this.cancel();
      return;
    }
    this.drag.dx = dx;
    if (Math.abs(dx) < 8 && !this.drag.moved) return;
    this.drag.moved = true;
    this.motion.style.transform = `translateX(${dx}px) rotate(${dx / 24}deg)`;
    this.labels(dx);
  }

  up(event) {
    if (!this.drag || event.pointerId !== this.drag.id) return;
    const { dx, moved } = this.drag;
    this.drag = null;
    if (moved) this.suppressUntil = performance.now() + 350;
    this.labels(0);
    if (moved && Math.abs(dx) > Math.max(65, this.card.clientWidth * .22)) {
      void this.grade(dx > 0);
    } else {
      void this.settle();
    }
  }

  labels(dx) {
    document.querySelector("#swipe-wrong").style.opacity = String(Math.min(1, Math.max(0, -dx / 130)));
    document.querySelector("#swipe-right").style.opacity = String(Math.min(1, Math.max(0, dx / 130)));
  }

  cancel() {
    this.drag = null;
    this.labels(0);
    this.motion.style.transform = "";
  }

  async settle() {
    const from = this.motion.style.transform || "none";
    const animation = this.motion.animate(
      [{ transform: from }, { transform: "translateX(0) rotate(0)" }],
      { duration: reducedMotion() ? 0 : 200, easing: "ease-out" },
    );
    this.motion.style.transform = "";
    await animation.finished;
  }

  async dismiss(right, soleCard) {
    this.drag = null;
    this.labels(0);
    const from = this.motion.style.transform || "none";
    if (!right && soleCard) {
      await this.settle();
      return;
    }
    const distance = Math.min(innerWidth, 650);
    const exit = right ? `translateX(${distance}px) rotate(14deg)` :
      `translateX(-${distance * .65}px) rotate(-12deg)`;
    const animation = this.motion.animate([
      { transform: from, opacity: 1 },
      { transform: exit, opacity: right ? 0 : .5 },
    ], {
      duration: reducedMotion() ? 0 : right ? 240 : 170,
      easing: "cubic-bezier(.2,.65,.3,1)", fill: "forwards",
    });
    await animation.finished;
    if (!right) {
      this.card.classList.add("no-flip");
      this.setFlipped(false);
      this.motion.style.zIndex = "-1";
      const returnToBottom = this.motion.animate([
        { transform: exit, opacity: .5 },
        { transform: "translate(8px, 16px) scale(.94) rotate(3deg)", opacity: 1 },
      ], {
        duration: reducedMotion() ? 0 : 170,
        easing: "ease-out", fill: "forwards",
      });
      await returnToBottom.finished;
      returnToBottom.cancel();
    }
    animation.cancel();
    this.motion.style.opacity = "0";
    this.motion.style.transform = "";
  }
}
