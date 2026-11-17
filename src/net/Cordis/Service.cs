// -----------------------------------------------------------------------
// <copyright file="Service.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Base class for a plugin that publishes itself as a named service. The instance registers in
/// its own fiber's store, so unloading the plugin removes the service and reloads every dependent.
/// </summary>
public abstract class Service : IContextScoped
{
    /// <summary>
    /// Initializes a new instance of the <see cref="Service"/> class. Provides the instance under
    /// <paramref name="name"/> in the fiber of <paramref name="ctx"/>.
    /// </summary>
    /// <param name="ctx">The context of the plugin that owns the service.</param>
    /// <param name="name">The service name to provide the instance under.</param>
    protected Service(Context ctx, string name)
    {
        this.Ctx = ctx;
        this.Name = name;
        this.Ctx.Provide(name, this, this.CheckAvailable);
    }

    /// <summary>
    /// Initializes a new instance of the <see cref="Service"/> class. Derives the service name from
    /// the concrete type (<see cref="ServiceNameAttribute"/> or full name) and provides the instance
    /// under it in the fiber of <paramref name="ctx"/>.
    /// </summary>
    /// <param name="ctx">The context of the plugin that owns the service.</param>
    protected Service(Context ctx)
    {
        this.Ctx = ctx;
        this.Name = ServiceNames.For(this.GetType());
        this.Ctx.Provide(this.Name, this, this.CheckAvailable);
    }

    /// <summary>Gets the service name this instance is provided under.</summary>
    public string Name { get; }

    /// <summary>Gets the context this instance is bound to. Rebound per caller by <see cref="ForContext"/>.</summary>
    protected Context Ctx { get; private set; }

    /// <summary>
    /// Gets a value indicating whether the service opts in to per-caller cloning. Only safe for stateless facades: the clone is shallow, so
    /// any field the service mutates after cloning (including event subscriptions) is a copy and
    /// will not be seen by other callers. Default is <c>false</c> — one shared instance.
    /// </summary>
    protected virtual bool ScopePerCaller => false;

    /// <summary>
    /// Shallow clone bound to the caller's context, so effects and log lines created inside
    /// service methods are attributed to the caller's fiber. Mirrors <c>Object.create(this)</c>.
    /// Returns this instance unless <see cref="ScopePerCaller"/> is <see langword="true"/>.
    /// </summary>
    /// <param name="ctx">The calling context.</param>
    /// <returns>A clone bound to <paramref name="ctx"/>, or this instance if cloning is off or <paramref name="ctx"/> is already its context.</returns>
    public virtual object ForContext(Context ctx)
    {
        if (!this.ScopePerCaller || ReferenceEquals(ctx, this.Ctx))
        {
            return this;
        }

        var clone = (Service)this.MemberwiseClone();
        clone.Ctx = ctx;
        return clone;
    }

    /// <summary>Dynamic availability check; a false result unloads dependents.</summary>
    /// <returns><see langword="true"/> if the service is available; otherwise, <see langword="false"/>. The default is always <see langword="true"/>.</returns>
    protected virtual bool CheckAvailable() => true;
}
