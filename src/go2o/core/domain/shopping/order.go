/**
 * Copyright 2014 @ ops Inc.
 * name :
 * author : newmin
 * date : 2013-12-09 15:03
 * description :
 * history :
 */

package shopping

import (
	"errors"
	"fmt"
	"go2o/core/domain/interface/enum"
	"go2o/core/domain/interface/member"
	"go2o/core/domain/interface/partner"
	"go2o/core/domain/interface/promotion"
	"go2o/core/domain/interface/shopping"
	"go2o/core/infrastructure"
	"go2o/core/infrastructure/log"
	"go2o/share/variable"
	"strings"
	"time"
)

var (
	EXP_BIT float32
)

type Order struct {
	_shopping   shopping.IShopping
	value       *shopping.ValueOrder
	cart        shopping.ICart
	coupons     []promotion.ICoupon
	memberRep   member.IMemberRep
	shoppingRep shopping.IShoppingRep
	partnerRep  partner.IPartnerRep
    _internalSuspend bool
}

func newOrder(shopping shopping.IShopping, value *shopping.ValueOrder, cart shopping.ICart,
	partnerRep partner.IPartnerRep, shoppingRep shopping.IShoppingRep,
	memberRep member.IMemberRep) shopping.IOrder {
	return &Order{
		_shopping:   shopping,
		value:       value,
		cart:        cart,
		memberRep:   memberRep,
		shoppingRep: shoppingRep,
		partnerRep:  partnerRep,
	}
}

func (this *Order) GetDomainId() int {
	return this.value.Id
}

func (this *Order) GetValue() shopping.ValueOrder {
	return *this.value
}

func (this *Order) ApplyCoupon(coupon promotion.ICoupon) error {
	if this.coupons == nil {
		this.coupons = []promotion.ICoupon{}
	}
	this.coupons = append(this.coupons, coupon)

	//val := coupon.GetValue()
	v := this.value
	//v.CouponCode = val.Code
	//v.CouponDescribe = coupon.GetDescribe()
	v.CouponFee = coupon.GetCouponFee(v.Fee)
	v.PayFee = v.Fee - v.CouponFee
	v.DiscountFee = v.DiscountFee + v.CouponFee
	return nil
}

// 获取应用的优惠券
func (this *Order) GetCoupons() []promotion.ICoupon {
	if this.coupons == nil {
		return make([]promotion.ICoupon, 0)
	}
	return this.coupons
}

// 添加备注
func (this *Order) AddRemark(remark string) {
	this.value.Note = remark
}

// 设置Shop
func (this *Order) SetShop(shopId int) error {
	//todo:验证Shop
	this.value.ShopId = shopId
	return nil
}

// 设置支付方式
func (this *Order) SetPayment(payment int) {
	this.value.PaymentOpt = payment
}

// 标记已支付
func (this *Order) SignPaid() error {
	unix := time.Now().Unix()
	this.value.IsPaid = 1
	this.value.UpdateTime = unix
	this.value.PaidTime = unix
	_, err := this.Save()
	return err
}

// 设置配送地址
func (this *Order) SetDeliver(deliverAddrId int) error {
	d := this.memberRep.GetDeliverAddr(this.value.MemberId, deliverAddrId)
	if d != nil {
		v := this.value
		v.DeliverAddress = d.Address
		v.DeliverName = d.RealName
		v.DeliverPhone = d.Phone
		v.DeliverTime = time.Now().Add(-time.Hour).Unix()
		return nil
	}
	return errors.New("Deliver address not exist!")
}

// 提交订单，返回订单号。如有错误则返回
func (this *Order) Submit() (string, error) {
	if this.GetDomainId() != 0 {
		return "", errors.New("订单不允许重复提交！")
	}

	if this.cart == nil || len(this.cart.GetValue().Items) == 0 {
		return "", errors.New("购物车为空！")
	}

	v := this.value
	v.CreateTime = time.Now().Unix()
	v.UpdateTime = v.CreateTime
	v.ItemsInfo = this.cart.GetSummary()
	v.OrderNo = this._shopping.GetFreeOrderNo()

	// 应用优惠券
	var err error
	var t *promotion.ValueCouponTake
	var b *promotion.ValueCouponBind
	for _, c := range this.GetCoupons() {
		if c.CanTake() {
			t, err = c.GetTake(v.MemberId)
			if err == nil {
				err = c.ApplyTake(t.Id)
			}
		} else {
			b, err = c.GetBind(v.MemberId)
			if err == nil {
				err = c.UseCoupon(b.Id)
			}
		}
		if err != nil {
			log.PrintErr(err)
			err = errors.New("Code 105:优惠券使用失败")
			return "", err
		}
	}

	// 保存订单
	id, err := this.shoppingRep.SaveOrder(this._shopping.GetAggregateRootId(), v)
	v.Id = id
	if err == nil {
		var oc *shopping.OrderCoupon = new(shopping.OrderCoupon)
		for _, c := range this.GetCoupons() {
			oc.Clone(c, v.Id, v.Fee)
			// 绑定订单与优惠券
			this.shoppingRep.SaveOrderCouponBind(oc)
		}
	}

	return v.OrderNo, err
}

// 保存订单
func (this *Order) Save() (int, error) {
	// 有操作后解除挂起状态
	if this.value.IsSuspend == 1 {
		this.value.IsSuspend = 0
	}

	if this.value.Id > 0 {
		return this.shoppingRep.SaveOrder(
			this._shopping.GetAggregateRootId(), this.value)
	}
	return 0, errors.New("please use Order.Submit() save new order.")
}

// 添加日志
func (this *Order) AppendLog(t enum.OrderLogType, system bool, message string) error {
	if this.GetDomainId() <= 0 {
		return errors.New("order not created.")
	}

	var systemInt int
	if system {
		systemInt = 1
	} else {
		systemInt = 0
	}

	var ol *shopping.OrderLog = &shopping.OrderLog{
		OrderId:    this.GetDomainId(),
		Type:       int(t),
		IsSystem:   systemInt,
		Message:    message,
		RecordTime: time.Now().Unix(),
	}
	return this.shoppingRep.SaveOrderLog(ol)
}

// 订单是否已完成
func (this *Order) IsOver() bool {
	s := this.value.Status
	return s == enum.ORDER_CANCEL || s == enum.ORDER_COMPLETED
}

// 处理订单
func (this *Order) Process() error {
	dt := time.Now()
	this.value.Status += 1
	this.value.UpdateTime = dt.Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, false, "订单处理中")
	}
	return err
}

// 确认订单
func (this *Order) Confirm() error {
	this.value.Status = enum.ORDER_CONFIRMED
	this.value.UpdateTime = time.Now().Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, false, "订单已经确认")
	}
	return err
}

// 配送订单
func (this *Order) Deliver() error {
	dt := time.Now()
	this.value.Status += 1
	this.value.DeliverTime = dt.Unix()
	this.value.UpdateTime = dt.Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, false, "订单开始配送")
	}
	return err
}

// 取消订单
func (this *Order) Cancel(reason string) error {
	if len(strings.TrimSpace(reason)) == 0 {
		return errors.New("取消原因不能为空")
	}
	status := this.value.Status
	if status == enum.ORDER_COMPLETED {
		return errors.New("订单已经完成!")
	}
	if status == enum.ORDER_CANCEL {
		return errors.New("订单已经被取消!")
	}

	this.value.Status = enum.ORDER_CANCEL
	this.value.UpdateTime = time.Now().Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, true, "订单已取消,原因："+reason)
	}

	return err
}

// 挂起
func (this *Order) Suspend(reason string) error {
	this.value.IsSuspend = 1
	this.value.UpdateTime = time.Now().Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, true, "订单已锁定"+reason)
	}
	return err
}

// 标记收货
func (this *Order) SignReceived() error {
	dt := time.Now()
	this.value.Status = enum.ORDER_RECEIVED
	this.value.UpdateTime = dt.Unix()

	_, err := this.Save()
	if err == nil {
		err = this.AppendLog(enum.ORDER_LOG_SETUP, false, "已收货")
	}
	return err
}

// 完成订单
func (this *Order) Complete() error {
	now := time.Now().Unix()
	v := this.value
	m, err := this.memberRep.GetMember(v.MemberId)
	if err == nil {
		var ptl partner.IPartner
		ptl, err = this.partnerRep.GetPartner(v.PartnerId)
		if err != nil {
			log.Println("供应商异常!", v.PartnerId)
			log.PrintErr(err)
			return err
		}

		pv := ptl.GetValue()
		if pv.ExpiresTime < time.Now().Unix() {
			return errors.New("您的账户已经过期!")
		}

		//返现比例
		saleConf := ptl.GetSaleConf()
		back_fee := v.Fee * saleConf.CashBackPercent

		//将此次消费记入会员账户
		this.updateShoppingMemberAccount(ptl, m,
			back_fee*saleConf.CashBackMemberPercent, now)

		//todo: 增加阶梯的返积分,比如订单满30送100积分
		backIntegral := int(v.Fee)*saleConf.IntegralBackNum +
			saleConf.IntegralBackExtra

		//判断是否满足升级条件
		if backIntegral != 0 {
			err = m.AddIntegral(v.PartnerId, enum.INTEGRAL_TYPE_ORDER,
				backIntegral, fmt.Sprintf("订单返积分%d个", backIntegral))
			if err != nil {
				return err
			}
		}

		// 增加经验
		if EXP_BIT == 0 {
			fv := infrastructure.GetContext().
				Config().GetFloat(variable.EXP_BIT)
			EXP_BIT = float32(fv)
		}

		if EXP_BIT == 0 {
			log.Println("[WANNING]:Exp_bit not set!")
		}

		err = m.AddExp(int(v.Fee * EXP_BIT))
		if err != nil {
			return err
		}

		// 三级返现
		this.backFor3R(ptl, m, back_fee, now)

		this.value.Status = enum.ORDER_COMPLETED
		this.value.IsSuspend = 0
		this.value.UpdateTime = now

		_, err := this.Save()

		if err == nil {
			err = this.AppendLog(enum.ORDER_LOG_SETUP, false, "订单已完成")
		}
	}
	return err
}

// 更新会员账户
func (this *Order) updateShoppingMemberAccount(pt partner.IPartner,
	m member.IMember, fee float32, unixTime int64) {
	if fee == 0 {
		return
	}
	v := this.GetValue()
	pv := pt.GetValue()
	//更新账户
	acc := m.GetAccount()
	acc.TotalFee = acc.TotalFee + this.value.Fee
	acc.TotalPay = acc.TotalPay + this.value.PayFee
	acc.PresentBalance = acc.PresentBalance + fee //更新赠送余额
	acc.UpdateTime = unixTime
	m.SaveAccount()

	//给自己返现
	icLog := &member.IncomeLog{
		MemberId:   this.value.MemberId,
		OrderId:    v.Id,
		Type:       "backcash",
		Fee:        fee,
		Log:        fmt.Sprintf("订单:%s(商家:%s)返现￥%.2f元", v.OrderNo, pv.Name, fee),
		State:      1,
		RecordTime: unixTime,
	}
	m.SaveIncomeLog(icLog)
}

// 三级返现
func (this *Order) backFor3R(pt partner.IPartner, m member.IMember,
	back_fee float32, unixTime int64) {
	if back_fee == 0 {
		return
	}

	i := 0
	mName := m.GetValue().Name
	saleConf := pt.GetSaleConf()
	percent := saleConf.CashBackTg2Percent
	for i < 2 {
		rl := m.GetRelation()
		if rl == nil || rl.TgId == 0 {
			break
		}

		m, _ = this.memberRep.GetMember(rl.TgId)
		if m == nil {
			break
		}

		if i == 1 {
			percent = saleConf.CashBackTg1Percent
		}

		this.updateMemberAccount(m, pt.GetValue().Name, mName,
			back_fee*percent, unixTime)
		i++
	}
}

func (this *Order) updateMemberAccount(m member.IMember,
	ptName, mName string, fee float32, unixTime int64) {
	if fee == 0 {
		return
	}

	//更新账户
	acc := m.GetAccount()
	acc.PresentBalance = acc.PresentBalance + fee
	acc.UpdateTime = unixTime
	m.SaveAccount()

	//给自己返现
	icLog := &member.IncomeLog{
		MemberId: this.value.MemberId,
		Type:     "backcash",
		Fee:      fee,
		Log: fmt.Sprintf("订单:%s(商家:%s,会员:%s)收入￥%.2f元",
			this.value.OrderNo, ptName, mName, fee),
		State:      1,
		RecordTime: unixTime,
	}
	m.SaveIncomeLog(icLog)
}
