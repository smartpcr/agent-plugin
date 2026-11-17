// -----------------------------------------------------------------------
// <copyright file="Service{TSelf}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Convenience base that derives the service name from <typeparamref name="TSelf"/>.</summary>
/// <typeparam name="TSelf">The concrete service type; its <see cref="ServiceNames.For{T}"/> name is the service name.</typeparam>
/// <param name="ctx">The context of the plugin that owns the service.</param>
public abstract class Service<TSelf>(Context ctx) : Service(ctx, ServiceNames.For<TSelf>())
    where TSelf : Service<TSelf>;
